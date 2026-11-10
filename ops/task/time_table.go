package task

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// TimeFlagsIn are TIME_TRACKING_DESIGN 10's sort and filter flags for a
// task table outside task list (mission show), the same names and rules
// as task list's (time_list.go).
type TimeFlagsIn struct {
	TimeSort    []string `json:"sort,omitempty" flag:"sort" desc:"order the task table: time_spent or elapsed, :asc (default) or :desc"`
	WorkedBy    string   `json:"worked_by,omitempty" flag:"worked-by" desc:"only tasks with time by this actor (@me, act_ id, key or name); add +agents for their agents too"`
	WorkedSince string   `json:"worked_since,omitempty" flag:"worked-since" desc:"only tasks with time on or after this day; TIME shows that range"`
	WorkedUntil string   `json:"worked_until,omitempty" flag:"worked-until" desc:"only tasks with time on or before this day; TIME shows that range"`
	MinTime     string   `json:"min_time,omitempty" flag:"min-time" desc:"only tasks with at least this effort (e.g. 2h)"`
	MaxTime     string   `json:"max_time,omitempty" flag:"max-time" desc:"only tasks with at most this effort (e.g. 30m)"`
}

// TimeTable applies in to a task table (TIME 10, FA-TASK-5):
//
//	timeQueryOf(in) ─► none given? → briefs unchanged, false
//	                └► timeIndex per row → keepTime → timeOrder (stable)
//	                   → each kept brief carries Time; shown = true
//
// rows are the briefs' live task rows; a brief without its row (a trashed
// task) is dropped once any flag is given (DQ10). --sort takes only the
// time keys: the table has no other order to combine with.
func TimeTable(ctx context.Context, env work.Env, in TimeFlagsIn, briefs []Brief, rows []row.Row) ([]Brief, bool, error) {
	o := ops{env: env}
	q, rest, err := o.timeQueryOf(ctx, ListIn{WorkedBy: in.WorkedBy, WorkedSince: in.WorkedSince, WorkedUntil: in.WorkedUntil, MinTime: in.MinTime, MaxTime: in.MaxTime}, in.TimeSort)
	if err != nil {
		return nil, false, err
	}
	if len(rest) > 0 {
		return nil, false, errs.New(errs.InvalidInput, "--sort "+strings.Join(rest, ",")+": a mission's task table sorts by time_spent or elapsed only").
			WithHint("e.g. --sort time_spent:desc; other orders: lore task list --mission <id> --sort …")
	}
	if !q.active() {
		return briefs, false, nil
	}
	byID := make(map[string]row.Row, len(rows))
	for _, r := range rows {
		byID[r.ID.String()] = r
	}
	timeOf := o.timeIndex(ctx, q)
	type pair struct {
		b Brief
		r row.Row
	}
	var kept []pair
	for _, b := range briefs {
		r, ok := byID[b.ID]
		if !ok {
			continue
		}
		t := timeOf(r)
		if !q.keepTime(t) {
			continue
		}
		b.Time = t.TimeBrief
		kept = append(kept, pair{b, r})
	}
	order := q.timeOrder(timeOf, nil)
	slices.SortStableFunc(kept, func(a, b pair) int { return order(a.r, b.r) })
	out := make([]Brief, 0, len(kept))
	for _, p := range kept {
		out = append(out, p.b)
	}
	return out, true, nil
}
