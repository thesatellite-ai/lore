package task

// Read views over tasks (TASK 13, 16.3, 18.4, 18.5):
//
//	list --sort urgency ─► urgencySort (pull it out of XQ10 specs)
//	                    ─► urgencyOrder: urgency.Compute over the forest
//	task board ─► list (same flags) ─► group by status|assignee|priority|
//	              mission|repo|parent ─► columns (empty groups of closed
//	              sets kept) ─► read-only (moving a card is task edit)
//	task stale ─► open tasks untouched for --days (tasks.stale_days, 60)
//	            minus claimed, active mission, future due, open children,
//	            recurring ─► --close: preview + one bat_ batch, cancel with
//	            close_reason stale (18.5)
//	slaState ─► tasks.sla.<priority> hours vs created → done|now, minus
//	            deferred time from the deferred_until history (18.4)

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/filter"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/sortspec"
	"github.com/thesatellite-ai/lore/logic/urgency"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names.
const (
	OpBoard = "task.board"
	OpStale = "task.stale"
)

// keyUrgency is the computed sort key of task list (13, OV15 leftover).
const keyUrgency = "urgency"

// defaultStaleDays is task stale's --days without tasks.stale_days (18.5).
const defaultStaleDays = 60

// slaAtRiskPercent: at_risk from 80% of the SLA (18.4).
const slaAtRiskPercent = 80

// SLA states (18.4).
const (
	SLANone     = ""
	SLAOK       = "ok"
	SLAAtRisk   = "at_risk"
	SLABreached = "breached"
)

// Board groupings (16.3, XQ8: grouping is --group-by).
const (
	GroupStatus   = "status"
	GroupAssignee = "assignee"
	GroupPriority = "priority"
	GroupMission  = "mission"
	GroupRepo     = "repo"
	GroupParent   = "parent"
)

// GroupBys lists the board groupings.
func GroupBys() []string {
	return []string{GroupStatus, GroupAssignee, GroupPriority, GroupMission, GroupRepo, GroupParent}
}

// noGroup is the column of tasks without the grouped field.
const noGroup = "(none)"

// BoardIn is the input of task.board: task list's flags plus --group-by.
type BoardIn struct {
	ListIn
	GroupBy string `json:"group_by,omitempty" flag:"group-by" desc:"status (default) | assignee | priority | mission | repo | parent"`
}

// BoardGroup is one column of the board.
type BoardGroup struct {
	Key   string  `json:"key"`
	Count int     `json:"count"`
	Tasks []Brief `json:"tasks"`
}

// BoardOut is the output of task.board.
type BoardOut struct {
	GroupBy string       `json:"group_by"`
	Groups  []BoardGroup `json:"groups"`
	Human   string       `json:"-"`
}

// Text implements op.Texter.
func (o BoardOut) Text() string { return o.Human }

// StaleIn is the input of task.stale.
type StaleIn struct {
	Days    int    `json:"days,omitempty" flag:"days" desc:"no change for this many days (default tasks.stale_days, else 60)"`
	Close   bool   `json:"close,omitempty" flag:"close" desc:"cancel them with close_reason stale (one batch; asks above batch.preview_rows)"`
	DryRun  bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"with --close: list what would be cancelled, write nothing"`
	Project string `json:"project,omitempty" flag:"project" desc:"only tasks of this project (prj_ id or name)"`
}

// StaleOut is the output of task.stale.
type StaleOut struct {
	Days   int     `json:"days"`
	Tasks  []Brief `json:"tasks"`
	Closed bool    `json:"closed,omitempty"`
	Batch  string  `json:"batch,omitempty"`
	Human  string  `json:"-"`
}

// Text implements op.Texter.
func (o StaleOut) Text() string { return o.Human }

func viewOps(o ops) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpBoard, Summary: "Show tasks as a board grouped by status, assignee, priority, mission, repo or parent (read-only)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: append(slices.Clone(work.ReadErrors), errs.Unsupported),
			Examples: []op.Example{{Title: "board by status", CLI: "lore task board --group-by status", Want: "todo"}},
		}, o.board),
		op.Define(op.Spec{
			Name: OpStale, Summary: "List open tasks with no change for N days; --close cancels them (close_reason stale)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), errs.OpenChildren),
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "stale for 60 days", CLI: "lore task stale --days 60", Want: "no change"}},
		}, o.stale),
	}
}

// urgencySort takes urgency out of the --sort values: 0 none, +1 asc, -1
// desc. It must come first (the other keys break its ties).
func urgencySort(raw []string) (int, []string, error) {
	specs, err := sortspec.Parse(raw)
	if err != nil {
		return 0, nil, err
	}
	dir := 0
	var rest []string
	for i, s := range specs {
		if s.Key != keyUrgency {
			rest = append(rest, s.String())
			continue
		}
		if i != 0 {
			return 0, nil, errs.New(errs.InvalidInput, "--sort urgency must be the first --sort key").WithHint("lore task list --sort urgency:desc --sort due_at")
		}
		dir = 1
		if s.Desc {
			dir = -1
		}
	}
	return dir, rest, nil
}

// urgencyOrder ranks by the urgency total (closed tasks score 0), then by
// then (the other --sort keys), then the stores' order (stable).
func (o ops) urgencyOrder(ctx context.Context, f forest, now time.Time, dir int, then func(a, b row.Row) int) (func(a, b row.Row) int, error) {
	next, targets, err := o.urgencyLookups(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]urgency.Task, 0, len(f.rows))
	for _, s := range f.rows {
		tasks = append(tasks, urgencyTaskOf(s.Row, next, targets))
	}
	w, err := o.urgencyWeights(ctx)
	if err != nil {
		return nil, err
	}
	scores := urgency.Compute(tasks, now, w)
	return func(a, b row.Row) int {
		c := dir * cmp.Compare(scores[a.ID.String()].Total, scores[b.ID.String()].Total)
		if c == 0 && then != nil {
			c = then(a, b)
		}
		return c
	}, nil
}

// board lists like task list and groups the result.
func (o ops) board(ctx context.Context, in BoardIn) (BoardOut, error) {
	// TASK 16.2: the view's group_by unless --group-by is given.
	lin, spec, err := o.applyView(ctx, in.ListIn)
	if err != nil {
		return BoardOut{}, err
	}
	in.ListIn = lin
	by := cmp.Or(in.GroupBy, spec.GroupBy, GroupStatus)
	if err = work.OneOf(Noun, "group-by", by, GroupBys()); err != nil {
		return BoardOut{}, err
	}
	rows := map[string]row.Row{}
	list, err := o.listRows(ctx, in.ListIn, rows)
	if err != nil {
		return BoardOut{}, err
	}
	var order []string
	switch by {
	case GroupStatus:
		order = slices.Clone(schema.TaskStatuses())
	case GroupPriority:
		order = slices.Clone(schema.TaskPriorities())
		slices.Reverse(order)
	}
	groups := map[string][]Brief{}
	for _, b := range list.Data {
		k := groupKey(rows[b.ID], by)
		if k == "" {
			k = noGroup
		}
		if !slices.Contains(order, k) {
			order = append(order, k)
		}
		groups[k] = append(groups[k], b)
	}
	out := BoardOut{GroupBy: by}
	var h strings.Builder
	for _, k := range order {
		g := BoardGroup{Key: k, Count: len(groups[k]), Tasks: groups[k]}
		if g.Tasks == nil {
			g.Tasks = []Brief{}
		}
		out.Groups = append(out.Groups, g)
		h.WriteString(k + " (" + strconv.Itoa(g.Count) + ")\n")
		for _, b := range g.Tasks {
			h.WriteString("  " + b.ID + "  " + b.Title + "\n")
		}
	}
	out.Human = h.String()
	return out, nil
}

// listRows runs task list and records each listed row by id (the board
// groups by fields the brief does not carry).
func (o ops) listRows(ctx context.Context, in ListIn, into map[string]row.Row) (ListOut, error) {
	list, err := o.list(ctx, in)
	if err != nil {
		return ListOut{}, err
	}
	all, err := work.ListAll(ctx, o.env, table, in.Store)
	if err != nil {
		return ListOut{}, err
	}
	want := map[string]bool{}
	for _, b := range list.Data {
		want[b.ID] = true
	}
	for _, s := range all {
		if want[s.Row.ID.String()] {
			into[s.Row.ID.String()] = s.Row
		}
	}
	return list, nil
}

// groupKey is r's value for a board grouping.
func groupKey(r row.Row, by string) string {
	switch by {
	case GroupAssignee:
		return work.Field(r, fieldAssigned)
	case GroupPriority:
		return work.Field(r, fieldPriority)
	case GroupMission:
		return work.Field(r, work.FieldMission)
	case GroupRepo:
		return work.Field(r, work.FieldRepo)
	case GroupParent:
		return work.Field(r, schema.FieldTaskParent)
	default:
		return work.Field(r, work.FieldStatus)
	}
}

// staleDays is --days, else tasks.stale_days, else 60.
func (o ops) staleDays(ctx context.Context, days int) (int, error) {
	if days < 0 {
		return 0, errs.New(errs.InvalidInput, "task stale: --days must be positive")
	}
	if days > 0 {
		return days, nil
	}
	p, ok := upgrade.As[bulk.Policier](o.env)
	if !ok {
		return defaultStaleDays, nil
	}
	set, err := p.Policy(ctx, "")
	if err != nil {
		return 0, err
	}
	if n, isSet := set.Limit(policy.KeyTasksStaleDays); isSet && n > 0 {
		return int(n), nil
	}
	return defaultStaleDays, nil
}

// stale lists (and with --close cancels) open tasks untouched for days.
func (o ops) stale(ctx context.Context, in StaleIn) (StaleOut, error) {
	if in.DryRun && !in.Close {
		return StaleOut{}, errs.New(errs.InvalidInput, "task stale: --dry-run needs --close")
	}
	days, err := o.staleDays(ctx, in.Days)
	if err != nil {
		return StaleOut{}, err
	}
	project, err := work.ProjectFilter(ctx, o.env, Noun, in.Project)
	if err != nil {
		return StaleOut{}, err
	}
	team, err := o.teamList(ctx, ListIn{})
	if err != nil {
		return StaleOut{}, err
	}
	f, err := o.forest(ctx, project)
	if err != nil {
		return StaleOut{}, err
	}
	activeMissions, err := o.activeMissions(ctx)
	if err != nil {
		return StaleOut{}, err
	}
	now := opclock.Now(o.env)
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	var hits []work.Sourced
	for _, s := range f.rows {
		r := s.Row
		switch {
		case project != "" && work.Field(r, work.FieldProject) != project,
			!open(r), r.UpdatedAt.After(cutoff),
			claimedNow(team, r),
			activeMissions[work.Field(r, work.FieldMission)],
			work.Time(r, fieldDue).After(now),
			hasOpenChild(f, r.ID.String()),
			r.Fields[fieldRecur] != nil && r.Fields[fieldRecur].Kind() != value.KindNull:
			continue
		}
		hits = append(hits, s)
	}
	slices.SortFunc(hits, func(a, b work.Sourced) int {
		return cmp.Or(a.Row.UpdatedAt.Compare(b.Row.UpdatedAt), cmp.Compare(a.Row.ID.String(), b.Row.ID.String()))
	})
	out := StaleOut{Days: days, Tasks: []Brief{}}
	items := make([]bulk.Item, 0, len(hits))
	for _, s := range hits {
		out.Tasks = append(out.Tasks, BriefOf(work.LabelerOf(o.env), s.Source, s.Row))
		items = append(items, bulk.Item{ID: s.Row.ID.String(), Label: work.Field(s.Row, work.FieldTitle)})
	}
	var h strings.Builder
	h.WriteString(strconv.Itoa(len(hits)) + " tasks with no change for " + strconv.Itoa(days) + " days (excluded: claimed, in an active mission, future due, open sub-tasks, recurring)\n")
	for _, b := range out.Tasks {
		h.WriteString("  " + b.ID + "  " + b.Title + "\n")
	}
	if !in.Close || len(hits) == 0 {
		out.Human = h.String()
		return out, nil
	}
	if in.DryRun {
		out.Human = h.String() + "would cancel them with close_reason stale (nothing written, --dry-run)\n"
		return out, nil
	}
	if err = bulk.Preview(ctx, o.env, hits[0].Source, "task stale --close", items); err != nil {
		return StaleOut{}, err
	}
	bctx, batch, err := bulk.Begin(ctx, bulk.KindTask)
	if err != nil {
		return StaleOut{}, err
	}
	for _, s := range hits {
		if _, err = o.cancel(bctx, CancelIn{ID: s.Row.ID.String(), Reason: schema.TaskCloseStale}); err != nil {
			return StaleOut{}, staleCloseErr(s.Row.ID.String(), batch.String(), err)
		}
	}
	out.Closed, out.Batch = true, batch.String()
	out.Human = work.Success + " " + strconv.Itoa(len(hits)) + " tasks cancelled (close_reason stale, batch " + batch.String() + "); lore batch show --id " + batch.String() + "\n"
	return out, nil
}

// staleCloseErr is the refusal of task stale --close when cancelling id
// fails part way through batch: the context first, then the cancel's own
// reason and hint (errs.Prefix; a bare Wrap printed the context only).
func staleCloseErr(id, batch string, err error) error {
	return errs.Prefix("task stale --close: "+id+" failed after earlier tasks of batch "+batch+" were cancelled", err)
}

// hasOpenChild reports whether task id has an open direct sub-task.
func hasOpenChild(f forest, id string) bool {
	for _, s := range f.rows {
		if work.Field(s.Row, schema.FieldTaskParent) == id && open(s.Row) {
			return true
		}
	}
	return false
}

// activeMissions is the set of active mission ids.
func (o ops) activeMissions(ctx context.Context) (map[string]bool, error) {
	ms, err := work.ListAll(ctx, o.env, schema.TableMissions, "")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, s := range ms {
		if !s.Row.Deleted && work.Field(s.Row, work.FieldStatus) == schema.MissionStatusActive {
			out[s.Row.ID.String()] = true
		}
	}
	return out, nil
}

// slaKeys maps a priority to its SLA policy key (18.4, OV22-SLA hours).
func slaKeys() map[string]policy.Key {
	return map[string]policy.Key{
		schema.TaskPriorityLow: policy.KeyTasksSLALow, schema.TaskPriorityMedium: policy.KeyTasksSLAMedium,
		schema.TaskPriorityHigh: policy.KeyTasksSLAHigh, schema.TaskPriorityUrgent: policy.KeyTasksSLAUrgent,
	}
}

// slaHours reads the SLA per priority for target (unset priorities absent).
func (o ops) slaHours(ctx context.Context, target store.Source) (map[string]int64, error) {
	p, ok := upgrade.As[bulk.Policier](o.env)
	if !ok {
		return map[string]int64{}, nil
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return nil, err
	}
	return SLAHours(set), nil
}

// SLAHours is the SLA in hours per priority of a policy set (18.4); a
// priority without a positive tasks.sla.<priority> is absent. Exported for
// the old binary's inbox, which reads the policy itself.
func SLAHours(set policy.Set) map[string]int64 {
	out := map[string]int64{}
	for prio, k := range slaKeys() {
		if n, isSet := set.Limit(k); isSet && n > 0 {
			out[prio] = n
		}
	}
	return out
}

// SLAClock is what the SLA state of one task is computed from (18.4).
type SLAClock struct {
	Priority, Status   string
	Created, Completed time.Time
	// DeferredUntil is the task's deferred_until now; History its history
	// (deferred_until changes are tracked, 17.2) for the deferred time.
	DeferredUntil time.Time
	History       []HistoryEntry
}

// slaClockOf is the SLA clock of a task row.
func slaClockOf(r row.Row) SLAClock {
	return SLAClock{
		Priority: work.Field(r, fieldPriority), Status: work.Field(r, work.FieldStatus),
		Created: r.CreatedAt, Completed: work.Time(r, work.FieldCompleted),
		DeferredUntil: work.Time(r, fieldDeferred), History: History(r),
	}
}

// slaState is the SLA state of r on read (18.4).
func slaState(r row.Row, hours map[string]int64, now time.Time) string {
	return SLAStateOf(slaClockOf(r), hours, now)
}

// SLALate is the open (not done, not cancelled, live) tasks among rows
// whose SLA is at risk or breached on read (18.4: doctor's SLA line), each
// list in rows' order; both empty when hours is empty.
func SLALate(rows []row.Row, hours map[string]int64, now time.Time) (atRisk, breached []string) {
	if len(hours) == 0 {
		return nil, nil
	}
	for _, r := range rows {
		if st := work.Field(r, work.FieldStatus); r.Deleted || st == schema.TaskStatusDone || st == schema.TaskStatusCancelled {
			continue
		}
		switch slaState(r, hours, now) {
		case SLAAtRisk:
			atRisk = append(atRisk, r.ID.String())
		case SLABreached:
			breached = append(breached, r.ID.String())
		}
	}
	return atRisk, breached
}

// SLAStateOf is the SLA state on read (18.4): elapsed from creation to done
// (or now), minus the time spent deferred (snoozed on purpose; blocked time
// still counts), against the priority's SLA; at_risk from 80%.
func SLAStateOf(c SLAClock, hours map[string]int64, now time.Time) string {
	st, _ := slaOf(c, hours, now)
	return st
}

// slaOf is the SLA state and the elapsed (deferred-free) time.
func slaOf(c SLAClock, hours map[string]int64, now time.Time) (string, time.Duration) {
	h, ok := hours[c.Priority]
	if !ok || c.Created.IsZero() {
		return SLANone, 0
	}
	end := now
	if (c.Status == schema.TaskStatusDone || c.Status == schema.TaskStatusCancelled) && !c.Completed.IsZero() {
		end = c.Completed
	}
	elapsed := end.Sub(c.Created) - deferredWithin(c, end)
	limit := time.Duration(h) * time.Hour
	switch {
	case elapsed > limit:
		return SLABreached, elapsed
	case elapsed*100 >= limit*slaAtRiskPercent:
		return SLAAtRisk, elapsed
	}
	return SLAOK, elapsed
}

// deferredWithin is the time between creation and end the task was
// deferred (18.4). The deferred_until history splits that span into
// segments, each with the value it held (the first entry's from is the
// value at creation; with no entry the current value held throughout); a
// segment counts as deferred from its start until the earlier of its value
// and its end.
func deferredWithin(c SLAClock, end time.Time) time.Duration {
	type seg struct {
		start time.Time
		until time.Time
	}
	segs := []seg{{start: c.Created, until: c.DeferredUntil}}
	first := true
	for _, e := range c.History {
		if e.Field != fieldDeferred {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil {
			continue
		}
		if first {
			segs[0].until, first = histTime(e.From), false
		}
		segs = append(segs, seg{start: at, until: histTime(e.To)})
	}
	var d time.Duration
	for i, s := range segs {
		segEnd := end
		if i+1 < len(segs) && segs[i+1].start.Before(segEnd) {
			segEnd = segs[i+1].start
		}
		stop := s.until
		if stop.After(segEnd) {
			stop = segEnd
		}
		if stop.After(s.start) {
			d += stop.Sub(s.start)
		}
	}
	return d
}

// slaWords is an SLA state as words ("at risk").
func slaWords(state string) string { return strings.ReplaceAll(state, "_", " ") }

// histTime parses a history datetime text; "" or bad text is zero.
func histTime(s string) time.Time {
	if dt, err := value.DateTimeOf(s); err == nil {
		return dt.Time()
	}
	return time.Time{}
}

// slaLine is the human SLA of a task with one: "SLA 48h · breached".
func slaLine(c SLAClock, hours map[string]int64, now time.Time) string {
	st, elapsed := slaOf(c, hours, now)
	if st == SLANone {
		return ""
	}
	return strconv.FormatInt(hours[c.Priority], 10) + "h · " + slaWords(st) + " · open " +
		strconv.FormatInt(int64(elapsed/time.Hour), 10) + "h (deferred time not counted)"
}

// keySLA is the computed --where key sla:ok|at_risk|breached (18.4).
const keySLA = "sla"

// SLAStates lists the --where sla values.
func SLAStates() []string { return []string{SLAOK, SLAAtRisk, SLABreached} }

// withSLA wraps a row getter so --where sla:… reads the SLA state on read.
func (o ops) withSLA(ctx context.Context, get func(row.Row) filter.Getter) (func(row.Row) filter.Getter, func(row.Row) string, error) {
	hours, err := o.slaHours(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	now := opclock.Now(o.env)
	state := func(r row.Row) string { return slaState(r, hours, now) }
	return func(r row.Row) filter.Getter {
		g := get(r)
		return func(k filter.Key) []string {
			if k.Name == keySLA && !k.Meta {
				if s := state(r); s != SLANone {
					return []string{s}
				}
				return nil
			}
			return g(k)
		}
	}, state, nil
}

// claimedNow reports an active claim on r (stale skips claimed work, 18.5).
func claimedNow(v teamView, r row.Row) bool {
	if v.board == nil {
		return false
	}
	_, claimed, active := v.board.Claim(r.ID.String())
	return claimed && active
}

// urgencyWeights are the 13.2 defaults with every tasks.urgency.<term> /
// tasks.urgency.priority.<level> a policy level sets (OV15B-URG: the
// nearest level wins, values in hundredths). Without a policy reader the
// defaults stand.
func (o ops) urgencyWeights(ctx context.Context) (urgency.Weights, error) {
	w := urgency.Default()
	p, ok := upgrade.As[bulk.Policier](o.env)
	if !ok {
		return w, nil
	}
	set, err := p.Policy(ctx, "")
	if err != nil {
		return urgency.Weights{}, err
	}
	return UrgencyWeights(set)
}

// UrgencyWeights applies a policy set's urgency weights to the 13.2
// defaults. Exported for the old binary's queue (saas/pkg/aicoder/queue),
// which reads the policy itself.
func UrgencyWeights(set policy.Set) (urgency.Weights, error) {
	w := urgency.Default()
	for _, k := range policy.UrgencyKeys() {
		h, isSet := set.Weight(k.Key)
		if !isSet {
			continue
		}
		if err := w.Set(urgency.Term(k.Term), k.Level, int(h)); err != nil {
			return urgency.Weights{}, errs.Wrap(errs.ConfigInvalid, "policy "+string(k.Key), err)
		}
	}
	return w, nil
}
