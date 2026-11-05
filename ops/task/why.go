package task

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/urgency"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// `task show --why` / `--who` (TASK 13.3, AGENT_IDENTITY 6.2):
//
//	show ─► forest(project) ─► urgencyInputs (tags `next`, mission target_date)
//	     ─► urgency.Compute ─► Full.Why (total + terms)
//	show ─► row envelope created/updated + actor names ─► Full.Who
//	     ├─ touched: created_by + history[].by + claim_log actors (synced)
//	     └─ Whoer (optional Env upgrade: the local session block, Q-LOCAL)

// nextTagName is the tag whose tasks get the next_tag term (13.2).
const nextTagName = "next"

// fieldTagName / fieldTargetDate are the tag and mission columns read.
const (
	fieldTagName    = "name"
	fieldTargetDate = "target_date"
)

// Why is the --why block: the urgency and every term.
type Why struct {
	Urgency string         `json:"urgency"`
	Terms   []urgency.Part `json:"terms"`
	// Closed: the task is closed, so it has no score.
	Closed bool `json:"closed,omitempty"`
	shown  bool
}

// Who is the --who block (the synced part; the session detail lives only
// on the writing machine, AGENT_IDENTITY 6.6, Q-LOCAL).
type Who struct {
	CreatedAt string   `json:"created_at,omitempty"`
	CreatedBy string   `json:"created_by,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	UpdatedBy string   `json:"updated_by,omitempty"`
	Sessions  []string `json:"sessions,omitempty"`
	// Touched is every actor that wrote the task, from the synced row:
	// creator, last writer, history entries and claim_log events
	// (AGENT_IDENTITY 6.2 "who changed a task"), most recent first.
	Touched []WhoActor `json:"touched,omitempty"`
	shown   bool
}

// WhoActor is one actor that touched a task.
type WhoActor struct {
	ActorID string `json:"actor_id"`
	Name    string `json:"name,omitempty"`
	// Changes counts the recorded writes: creation, history entries,
	// claim events (a write that kept no history entry is not counted).
	Changes int    `json:"changes"`
	Last    string `json:"last,omitempty"`
}

// Whoer is the optional Env upgrade that knows the local session of the
// writes to a row (tool, session, model, confirmed). Lines are already
// redacted by the host's rules (6.10: session ids off-terminal / in CI).
type Whoer interface {
	WhoLines(ctx context.Context, rowID string) ([]string, error)
}

// why computes the --why block for r over its project's tasks.
func (o ops) why(ctx context.Context, r row.Row) (Why, error) {
	if !open(r) {
		return Why{Closed: true, shown: true}, nil
	}
	f, err := o.forest(ctx, work.Field(r, work.FieldProject))
	if err != nil {
		return Why{}, err
	}
	next, targets, err := o.urgencyLookups(ctx)
	if err != nil {
		return Why{}, err
	}
	tasks := make([]urgency.Task, 0, len(f.rows))
	for _, s := range f.rows {
		tasks = append(tasks, urgencyTaskOf(s.Row, next, targets))
	}
	sc := urgency.Compute(tasks, opclock.Now(o.env), urgency.Default())[r.ID.String()]
	return Why{Urgency: urgency.Format(sc.Total), Terms: sc.Terms, shown: true}, nil
}

// urgencyLookups reads the `next` tag ids and the mission target dates.
func (o ops) urgencyLookups(ctx context.Context) (map[string]bool, map[string]time.Time, error) {
	tags, err := work.ListAll(ctx, o.env, schema.TableTags, "")
	if err != nil {
		return nil, nil, err
	}
	next := map[string]bool{}
	for _, s := range tags {
		if !s.Row.Deleted && strings.EqualFold(work.Field(s.Row, fieldTagName), nextTagName) {
			next[s.Row.ID.String()] = true
		}
	}
	ms, err := work.ListAll(ctx, o.env, schema.TableMissions, "")
	if err != nil {
		return nil, nil, err
	}
	targets := map[string]time.Time{}
	for _, s := range ms {
		if !s.Row.Deleted {
			targets[s.Row.ID.String()] = work.Time(s.Row, fieldTargetDate)
		}
	}
	return next, targets, nil
}

// urgencyTaskOf maps a task row to the score's input.
func urgencyTaskOf(r row.Row, next map[string]bool, targets map[string]time.Time) urgency.Task {
	t := urgency.Task{
		ID: r.ID.String(), Priority: urgency.Priority(work.Field(r, fieldPriority)), Status: urgency.Status(work.Field(r, work.FieldStatus)),
		CreatedAt: r.CreatedAt, Proposed: work.Field(r, fieldCommitment) == schema.TaskCommitmentProposed,
		Due: work.Time(r, fieldDue), Parent: work.Field(r, fieldParent),
	}
	if m := work.Field(r, "mission_id"); m != "" {
		t.MissionTarget = targets[m]
	}
	for _, d := range depsOf(r) {
		t.After = append(t.After, d.TaskID)
	}
	for _, id := range work.List(r, schema.FieldTagIDs) {
		if next[id] {
			t.NextTag = true
		}
	}
	return t
}

// who builds the --who block.
func (o ops) who(ctx context.Context, r row.Row) (Who, error) {
	w := Who{shown: true}
	if !r.CreatedAt.IsZero() {
		w.CreatedAt = r.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !r.UpdatedAt.IsZero() {
		w.UpdatedAt = r.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if !r.CreatedBy.IsZero() {
		w.CreatedBy = o.actorName(ctx, r.CreatedBy.String())
	}
	if !r.UpdatedBy.IsZero() {
		w.UpdatedBy = o.actorName(ctx, r.UpdatedBy.String())
	}
	w.Touched = o.touched(ctx, r)
	if wh, ok := upgrade.As[Whoer](o.env); ok {
		lines, err := wh.WhoLines(ctx, r.ID.String())
		if err != nil {
			return Who{}, err
		}
		w.Sessions = lines
	}
	return w, nil
}

// touched folds the row's writers (6.2): created_by at created_at, each
// history entry's by/at, each claim_log actor; updated_by joins at
// updated_at when no entry named it. Ordered by last write, newest first,
// then actor id.
func (o ops) touched(ctx context.Context, r row.Row) []WhoActor {
	by := map[string]*WhoActor{}
	note := func(actor, at string, count bool) {
		if !strings.HasPrefix(actor, "act_") {
			return
		}
		a, ok := by[actor]
		if !ok {
			a = &WhoActor{ActorID: actor}
			by[actor] = a
		}
		if count {
			a.Changes++
		}
		if at > a.Last {
			a.Last = at
		}
	}
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	note(r.CreatedBy.String(), stamp(r.CreatedAt), true)
	for _, e := range History(r) {
		at := e.At
		if t, err := time.Parse(histTimeLayout, e.At); err == nil {
			at = stamp(t)
		}
		note(e.By, at, true)
	}
	for _, c := range claimLog(r) {
		note(c.ActorID, stamp(c.At), true)
	}
	note(r.UpdatedBy.String(), stamp(r.UpdatedAt), false)
	out := make([]WhoActor, 0, len(by))
	for _, a := range by {
		a.Name = o.actorName(ctx, a.ActorID)
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b WhoActor) int { return cmp.Or(cmp.Compare(b.Last, a.Last), cmp.Compare(a.ActorID, b.ActorID)) })
	return out
}

// claimLog decodes the task's claim_log (nil when absent or unreadable).
func claimLog(r row.Row) []claimEntry {
	v := r.Fields[fieldClaimLog]
	if v == nil {
		return nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil
	}
	var log []claimEntry
	if json.Unmarshal(raw, &log) != nil {
		return nil
	}
	return log
}

// whyText / whoText are the human lines show appends.
func (w *Why) text() string {
	if w.Closed {
		return "  urgency:  - (closed)\n"
	}
	var b strings.Builder
	b.WriteString("  urgency:  " + w.Urgency + "\n")
	for _, l := range (urgency.Score{Terms: w.Terms}).Lines() {
		b.WriteString("    " + l + "\n")
	}
	return b.String()
}

func (w *Who) text() string {
	var b strings.Builder
	if w.CreatedAt != "" || w.CreatedBy != "" {
		b.WriteString("  created:  " + strings.TrimSpace(w.CreatedAt+"  "+w.CreatedBy) + "\n")
	}
	if w.UpdatedAt != "" || w.UpdatedBy != "" {
		b.WriteString("  updated:  " + strings.TrimSpace(w.UpdatedAt+"  "+w.UpdatedBy) + "\n")
	}
	if len(w.Touched) > 0 {
		b.WriteString("  touched by:\n")
		for _, a := range w.Touched {
			name := a.Name
			if name == "" {
				name = a.ActorID
			}
			b.WriteString("    " + padRight(name, 24) + "  " + strconv.Itoa(a.Changes) + " change(s), last " + a.Last + "\n")
		}
	}
	for _, l := range w.Sessions {
		b.WriteString("    " + l + "\n")
	}
	if len(w.Sessions) == 0 {
		b.WriteString("  (session detail lives only on the writing machine: lore show <id> --origin)\n")
	}
	return b.String()
}
