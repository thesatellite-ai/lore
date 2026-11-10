package task

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/sortspec"
	"github.com/thesatellite-ai/lore/logic/timecalc"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Time reporting on task list (TIME_TRACKING_DESIGN 10, DQ10):
//
//	list ─► timeQueryOf(flags) ─► (active?) timeIndex: every row's spans
//	        │                      → timecalc.Compute per task (in range)
//	        ├► keep: --worked-by / range / --min-time / --max-time
//	        ├► order: --sort time_spent | elapsed (pulled out of --sort)
//	        └► deco: TIME (+ELAPSED) column, --json time object
//
// Times are computed from the rows on read (open spans end now), so a list
// never depends on the cache being fresh.

// Sort keys of task list that are computed from time (design 10).
const (
	keyTimeSpent = "time_spent"
	keyElapsed   = "elapsed"
)

// Extra list columns of --columns (design 10).
const (
	colTime    = "time"
	colElapsed = "elapsed"
)

// agentsSuffix widens --worked-by to the person's agent actors (design 10).
const agentsSuffix = "+agents"

// TimeBrief is the --json time object of a listed task (design 10).
type TimeBrief struct {
	EffortSeconds  int64 `json:"effort_seconds"`
	ElapsedSeconds int64 `json:"elapsed_seconds"`
	ManualSeconds  int64 `json:"manual_seconds"`
}

// timeQuery is task list's time flags, parsed.
type timeQuery struct {
	workedBy       string
	withAgents     bool
	since, until   string
	minSet, maxSet bool
	min, max       int64
	sort           []sortspec.Spec // time_spent / elapsed keys, in order
	showTime       bool
	showElapsed    bool
	// columns are --columns' table columns (columns.go; empty = line mode).
	columns []string
}

// active reports whether any time flag was given (the TIME column then
// appears automatically, design 10).
func (q timeQuery) active() bool {
	return q.workedBy != "" || q.since != "" || q.until != "" || q.minSet || q.maxSet || len(q.sort) > 0 || q.showTime || q.showElapsed
}

// ranged reports a --worked-since / --worked-until window.
func (q timeQuery) ranged() bool { return q.since != "" || q.until != "" }

// splitTimeSort pulls the time sort keys out of --sort: they are computed,
// not task fields, so the field sort never sees them.
func splitTimeSort(raw []string) ([]sortspec.Spec, []string, error) {
	specs, err := sortspec.Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	var timeKeys []sortspec.Spec
	var rest []string
	for _, s := range specs {
		if s.Key == keyTimeSpent || s.Key == keyElapsed {
			timeKeys = append(timeKeys, s)
			continue
		}
		rest = append(rest, s.String())
	}
	return timeKeys, rest, nil
}

// timeQueryOf parses the time flags of in; sort is the --sort list (it
// returns the field sort keys left over).
func (o ops) timeQueryOf(ctx context.Context, in ListIn, sort []string) (timeQuery, []string, error) {
	var q timeQuery
	var err error
	if q.sort, sort, err = splitTimeSort(sort); err != nil {
		return q, nil, err
	}
	if in.WorkedBy != "" {
		ref := strings.TrimSpace(in.WorkedBy)
		if base, ok := strings.CutSuffix(ref, agentsSuffix); ok {
			ref, q.withAgents = base, true
		}
		if q.workedBy, err = o.actorRef(ctx, ref); err != nil {
			return q, nil, err
		}
	}
	clk, err := o.clock(ctx, store.SourceRepo)
	if err != nil {
		return q, nil, err
	}
	if q.since, err = dayText(clk, "worked-since", in.WorkedSince); err != nil {
		return q, nil, err
	}
	if q.until, err = dayText(clk, "worked-until", in.WorkedUntil); err != nil {
		return q, nil, err
	}
	for _, d := range []struct {
		flag, raw string
		set       *bool
		dst       *int64
	}{{"min-time", in.MinTime, &q.minSet, &q.min}, {"max-time", in.MaxTime, &q.maxSet, &q.max}} {
		if strings.TrimSpace(d.raw) == "" {
			continue
		}
		v, derr := value.DurationOf(strings.TrimSpace(d.raw))
		if derr != nil || v.Duration() < 0 {
			return q, nil, errs.New(errs.DurationBad, "--"+d.flag+" "+work.Quote(d.raw)+" is not a duration").WithHint("give e.g. 45m, 2h or 1h30m")
		}
		*d.set, *d.dst = true, int64(v.Duration()/time.Second)
	}
	// TASK 16.2: bare names make a column table; +time / +elapsed alone
	// extend the v0.1.17 line (columns.go).
	cs, err := parseColumns(in.Columns, taskKeys())
	if err != nil {
		return q, nil, err
	}
	q.columns, q.showTime, q.showElapsed = cs.named, cs.time, cs.elapsed
	return q, sort, nil
}

// taskTime is one task's time for a list.
type taskTime struct {
	TimeBrief
	// worked: the task has time in the range (by --worked-by, when given).
	worked bool
}

// timeIndex returns the time of a row under q, computed once per task
// (open spans end at now).
func (o ops) timeIndex(ctx context.Context, q timeQuery) func(row.Row) taskTime {
	owners := o.actorOwners(ctx)
	canon := o.mergedActors(ctx) // TIME 11.11
	ownerOf := func(a string) string {
		if v := owners[a]; v != "" {
			return v
		}
		return a
	}
	now := opclock.Now(o.env).UTC()
	inRange := func(day string) bool {
		return (q.since == "" || day >= q.since) && (q.until == "" || day <= q.until)
	}
	byMatches := func(actor string) bool {
		return q.workedBy == "" || actor == q.workedBy || (q.withAgents && ownerOf(actor) == q.workedBy)
	}
	memo := map[string]taskTime{}
	return func(r row.Row) taskTime {
		if t, ok := memo[r.ID.String()]; ok {
			return t
		}
		var spans []timecalc.Span
		worked := false
		add := func(actor, session, day string, s timecalc.Span) {
			actor = canon(actor)
			if !inRange(day) {
				return
			}
			s.Worker, s.Owner = timecalc.Worker{Actor: actor, Session: session}, ownerOf(actor)
			spans = append(spans, s)
			worked = worked || byMatches(actor)
		}
		for _, e := range TimeEntries(r) {
			if e.Deleted() {
				continue
			}
			add(e.ActorID.String(), e.SessionRef, e.Day, timecalc.Span{Start: e.StartTime(), End: e.EndTime(), Seconds: e.Seconds, Manual: e.Source == timeentry.SourceManual})
		}
		for _, s := range TimeOpen(r) {
			add(s.ActorID.String(), "", s.StartTime().Format(timeentry.DayLayout), timecalc.Span{Start: s.StartTime(), End: now, Open: true})
		}
		res := timecalc.Compute(spans)
		if tt, ok := TimeCarried(r); ok { // F-MOVE-3: totals a client move carried
			res.Effort += tt.EffortSeconds
		}
		t := taskTime{TimeBrief: TimeBrief{EffortSeconds: res.Effort, ElapsedSeconds: res.Elapsed, ManualSeconds: res.Manual}, worked: worked}
		memo[r.ID.String()] = t
		return t
	}
}

// actorOwners maps an agent actor id to its owner (owner_actor_id).
func (o ops) actorOwners(ctx context.Context) map[string]string {
	all, err := work.ListAll(ctx, o.env, schema.TableActors, "")
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, s := range all {
		if own := work.Field(s.Row, "owner_actor_id"); own != "" {
			m[s.Row.ID.String()] = own
		}
	}
	return m
}

// mergedActors maps an actor id to the end of its merged_into chain
// (TIME 11.11: reports follow a merge; stored entries keep their id). An
// unmerged or unknown id maps to itself; a cycle stops where it repeats.
func (o ops) mergedActors(ctx context.Context) func(string) string {
	next := map[string]string{}
	if all, err := work.ListAll(ctx, o.env, schema.TableActors, ""); err == nil {
		for _, s := range all {
			if to := work.Field(s.Row, "merged_into"); to != "" && to != s.Row.ID.String() {
				next[s.Row.ID.String()] = to
			}
		}
	}
	return func(id string) string {
		seen := map[string]bool{id: true}
		for {
			to, ok := next[id]
			if !ok || seen[to] {
				return id
			}
			seen[to] = true
			id = to
		}
	}
}

// keepTime applies --worked-by / --worked-since/until / --min/--max-time.
func (q timeQuery) keepTime(t taskTime) bool {
	switch {
	case (q.workedBy != "" || q.ranged()) && !t.worked,
		q.minSet && t.EffortSeconds < q.min,
		q.maxSet && t.EffortSeconds > q.max:
		return false
	}
	return true
}

// timeOrder ranks by the time sort keys, then by then (other --sort keys).
func (q timeQuery) timeOrder(timeOf func(row.Row) taskTime, then func(a, b row.Row) int) func(a, b row.Row) int {
	return func(a, b row.Row) int {
		ta, tb := timeOf(a), timeOf(b)
		for _, s := range q.sort {
			va, vb := ta.EffortSeconds, tb.EffortSeconds
			if s.Key == keyElapsed {
				va, vb = ta.ElapsedSeconds, tb.ElapsedSeconds
			}
			c := cmp.Compare(va, vb)
			if s.Desc {
				c = -c
			}
			if c != 0 {
				return c
			}
		}
		if then != nil {
			return then(a, b)
		}
		return 0
	}
}

// timeColumns appends the TIME (and ELAPSED) column to a list line.
func (q timeQuery) timeColumns(b *Brief, t taskTime) {
	tb := t.TimeBrief
	b.Time = tb
	b.base += "  time:" + timeentry.FormatSeconds(tb.EffortSeconds)
	if q.showElapsed || slices.ContainsFunc(q.sort, func(s sortspec.Spec) bool { return s.Key == keyElapsed }) {
		b.base += "  elapsed:" + timeentry.FormatSeconds(tb.ElapsedSeconds)
	}
	b.Line = b.base + storeLabel(b.Label)
}

// trasher is the optional engine capability of --include-deleted (the
// trash reads hidden rows; DQ10).
type trasher interface {
	Trashed(ctx context.Context) ([]engine.Trashed, error)
}

// trashedTasks are the task rows in the trash of the attached stores
// (only: one store, "" every store). A surface without the trash refuses.
func (o ops) trashedTasks(ctx context.Context, only string) ([]work.Sourced, error) {
	t, ok := upgrade.As[trasher](o.env)
	if !ok {
		return nil, errs.New(errs.Unsupported, "task list --include-deleted: this surface has no trash to read")
	}
	all, err := t.Trashed(ctx)
	if err != nil {
		return nil, err
	}
	var out []work.Sourced
	for _, it := range all {
		if it.Row.Table != table || (only != "" && string(it.Store) != only) {
			continue
		}
		r := it.Row
		r.Deleted = false // listed like a live row; the line says where it is
		out = append(out, work.Sourced{Source: it.Store, Row: r})
	}
	return out, nil
}

// trashMark is the line suffix of a task listed from the trash.
const trashMark = "  (in the trash)"
