package task

// estimate.go — the built-in estimate (TASK_SYSTEM_DESIGN 9, TSQ4):
// --estimate / --clear-estimate on add and edit, the rollup in list, show
// and mission show, and the estimate line next to tracked time (9.3).
//
//	--estimate raw ─► estimateUnit (tasks.estimate_unit policy) ─► estimate.Parse ─► text literal
//	show/list ─► forest leaves under id ─► estimate.Sum ─► EstimateInfo (+ tracked seconds)
//
// A parent's own estimate is never added to its children's (9.2: double
// counting); unestimated leaves are counted so "13 left" never hides unknowns.

import (
	"context"
	"math"
	"strconv"

	"github.com/thesatellite-ai/lore/logic/estimate"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fieldEstimate is the task's estimate literal (schema.FieldTaskEstimate).
const fieldEstimate = schema.FieldTaskEstimate

// secondsPerHundredthHour converts tracked seconds to hundredths of an
// hour (36 s) for the hours ratio.
const secondsPerHundredthHour = 36

// EstimateInfo is the estimate of one task (9.2, 9.3): its own value, the
// subtree rollup of a parent, and tracked time beside it.
type EstimateInfo struct {
	Estimate string `json:"estimate,omitempty"`
	Unit     string `json:"estimate_unit,omitempty"`
	// Subtree is the sum of the estimated leaves under a parent (absent
	// for a task without sub-tasks), split done/open, and the leaves
	// with none.
	Subtree     string `json:"estimate_subtree,omitempty"`
	SubtreeDone string `json:"estimate_done,omitempty"`
	SubtreeOpen string `json:"estimate_open,omitempty"`
	Unestimated int    `json:"unestimated,omitempty"`
	// TrackedSeconds is the task's tracked time (the same totals as
	// lore task time; tombstoned entries left out, DQ10).
	TrackedSeconds int64 `json:"tracked_seconds,omitempty"`
	// Ratio is tracked/estimate, unit hours only (9.3).
	Ratio float64 `json:"ratio,omitempty"`
	unit  estimate.Unit
}

// estimateUnit is the effective tasks.estimate_unit for target (points
// when the Env has no policy reader).
func (o ops) estimateUnit(ctx context.Context, target store.Source) (estimate.Unit, error) {
	p, ok := upgrade.As[bulk.Policier](o.env)
	if !ok {
		return estimate.Points, nil
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return "", err
	}
	if e, isSet := set.Get(policy.KeyTasksEstimateUnit); isSet && e.Value != nil {
		return estimate.UnitOf(e.Value.String()), nil
	}
	return estimate.Points, nil
}

// setEstimate parses raw in target's unit into fields (no-op for "").
func (o ops) setEstimate(ctx context.Context, fields map[string]value.Value, target store.Source, raw string) error {
	if raw == "" {
		return nil
	}
	unit, err := o.estimateUnit(ctx, target)
	if err != nil {
		return err
	}
	lit, err := estimate.Parse(raw, unit)
	if err != nil {
		return err
	}
	fields[fieldEstimate], err = value.TextOf(lit)
	return err
}

// tracked is a row's tracked seconds (live entries).
func tracked(r row.Row) int64 { return timeentry.Sum(TimeEntries(r)).All }

// leavesUnder lists the leaf tasks under id (cancelled left out, 9.2);
// a task without children is its own only leaf.
func leavesUnder(f forest, id string) []estimate.Leaf {
	var out []estimate.Leaf
	for _, d := range f.tree.Descendants(id) {
		if len(f.tree.Children(d)) > 0 {
			continue
		}
		r := f.row(d).Row
		if stateOf(r) == tasktree.Cancelled {
			continue
		}
		out = append(out, estimate.Leaf{Literal: work.Field(r, fieldEstimate), Done: stateOf(r) == tasktree.Done})
	}
	return out
}

// estimateInfo builds the estimate of r (f may lack r: then no rollup).
func estimateInfo(f forest, r row.Row, unit estimate.Unit) EstimateInfo {
	info := EstimateInfo{Estimate: work.Field(r, fieldEstimate), TrackedSeconds: tracked(r), unit: unit}
	id := r.ID.String()
	if f.tree != nil && f.tree.Has(id) && len(f.tree.Children(id)) > 0 {
		t := estimate.Sum(leavesUnder(f, id))
		info.Subtree, info.SubtreeDone, info.SubtreeOpen = estimate.Format(t.All()), estimate.Format(t.Done), estimate.Format(t.Open)
		info.Unestimated = t.Unestimated
	}
	if info.Estimate == "" && info.Subtree == "" {
		return EstimateInfo{TrackedSeconds: info.TrackedSeconds}
	}
	info.Unit = string(unit)
	if h, ok := estimate.Hundredths(info.Estimate); ok && unit == estimate.Hours && h > 0 && info.TrackedSeconds > 0 {
		r := float64(info.TrackedSeconds) / secondsPerHundredthHour / float64(h)
		info.Ratio = math.Round(r*hundredth) / hundredth
	}
	return info
}

// hundredth rounds the ratio to two decimals.
const hundredth = 100

// Line is the human estimate line (9.3): "est 3h · tracked 4h 10m ·
// +1h 10m (139%)", a parent "own 8 · children 5 (done 2, open 3,
// unestimated 1)"; "" without an estimate.
func (e EstimateInfo) Line() string {
	if e.Unit == "" {
		return ""
	}
	h, own := estimate.Hundredths(e.Estimate)
	line := ""
	switch {
	case own && e.Subtree != "":
		line = "own " + estimate.Label(h, e.unit) + " · children " + e.Subtree
	case own:
		line = "est " + estimate.Label(h, e.unit)
	default:
		line = "children " + e.Subtree
	}
	if e.Subtree != "" {
		line += " (done " + e.SubtreeDone + ", open " + e.SubtreeOpen
		if e.Unestimated > 0 {
			line += ", unestimated " + strconv.Itoa(e.Unestimated)
		}
		line += ")"
	}
	if e.TrackedSeconds > 0 {
		line += " · tracked " + timeentry.FormatSeconds(e.TrackedSeconds)
		if own && e.unit == estimate.Hours && h > 0 {
			diff := e.TrackedSeconds - h*secondsPerHundredthHour
			sign := "+"
			if diff < 0 {
				sign, diff = "-", -diff
			}
			line += " · " + sign + timeentry.FormatSeconds(diff) + " (" + strconv.Itoa(int(math.Round(e.Ratio*hundredth))) + "%)"
		}
	}
	return line
}

// MissionEstimate is the estimate rollup of a mission's tasks (9.2, 9.3):
// leaf sums split done/open, unestimated leaves, tracked time and, for
// points, the time per point over done tasks that have both.
type MissionEstimate struct {
	Unit               string `json:"estimate_unit,omitempty"`
	Total              string `json:"estimate,omitempty"`
	Done               string `json:"estimate_done,omitempty"`
	Open               string `json:"estimate_open,omitempty"`
	Unestimated        int    `json:"unestimated,omitempty"`
	TrackedSeconds     int64  `json:"tracked_seconds,omitempty"`
	DoneTrackedSeconds int64  `json:"done_tracked_seconds,omitempty"`
	// PerPointSeconds is tracked seconds per estimate unit over done
	// leaves with both an estimate and tracked time (points only).
	PerPointSeconds int64 `json:"seconds_per_point,omitempty"`
	unit            estimate.Unit
}

// MissionRollup rolls up rows (a mission's live tasks) in src's unit.
func MissionRollup(ctx context.Context, env work.Env, src store.Source, rows []row.Row) (MissionEstimate, error) {
	unit, err := ops{env: env}.estimateUnit(ctx, src)
	if err != nil {
		return MissionEstimate{}, err
	}
	parents := map[string]bool{}
	for _, r := range rows {
		if p := work.Field(r, fieldParent); p != "" {
			parents[p] = true
		}
	}
	var leaves []estimate.Leaf
	m := MissionEstimate{unit: unit}
	var pointH, pointSec int64
	for _, r := range rows {
		st := stateOf(r)
		sec := tracked(r)
		m.TrackedSeconds += sec
		if st == tasktree.Done {
			m.DoneTrackedSeconds += sec
		}
		if parents[r.ID.String()] || st == tasktree.Cancelled {
			continue
		}
		lit := work.Field(r, fieldEstimate)
		leaves = append(leaves, estimate.Leaf{Literal: lit, Done: st == tasktree.Done})
		if h, ok := estimate.Hundredths(lit); ok && h > 0 && sec > 0 && st == tasktree.Done {
			pointH, pointSec = pointH+h, pointSec+sec
		}
	}
	t := estimate.Sum(leaves)
	if t.All() == 0 && t.Unestimated == len(leaves) {
		return MissionEstimate{TrackedSeconds: m.TrackedSeconds, DoneTrackedSeconds: m.DoneTrackedSeconds}, nil
	}
	m.Unit, m.Total, m.Done, m.Open, m.Unestimated = string(unit), estimate.Format(t.All()), estimate.Format(t.Done), estimate.Format(t.Open), t.Unestimated
	if unit == estimate.Points && pointH > 0 {
		m.PerPointSeconds = pointSec * hundredth / pointH
	}
	return m, nil
}

// Lines are mission show's "estimate:" and "tracked:" lines (9.3).
func (m MissionEstimate) Lines() string {
	out := ""
	if m.Unit != "" {
		h, _ := estimate.Hundredths(m.Total)
		out += "  estimate: " + estimate.Label(h, m.unit) + " (done " + m.Done + ", open " + m.Open + ", unestimated " + strconv.Itoa(m.Unestimated) + " tasks)\n"
	}
	if m.TrackedSeconds > 0 {
		out += "  tracked:  " + timeentry.FormatSeconds(m.TrackedSeconds) + " (done tasks " + timeentry.FormatSeconds(m.DoneTrackedSeconds)
		if m.PerPointSeconds > 0 {
			out += " → " + timeentry.FormatSeconds(m.PerPointSeconds) + " per point"
		}
		out += "; open tasks " + timeentry.FormatSeconds(m.TrackedSeconds-m.DoneTrackedSeconds) + ")\n"
	}
	return out
}
