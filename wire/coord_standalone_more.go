package wire

// coord_standalone_more.go — task reassign and merged, hold claim and
// release, gate request and approve for a host without its own
// coordination machinery (cmd/lore, P3-SIGCOORD-2): v0.1.17's command
// bodies (saas coord_ops_run.go, task_merged.go, coord_cmd.go,
// gate_cmd.go) over the root signals and coord packages, for an unlinked
// repo.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Task and mission fields these verbs read or write.
const (
	fieldMergedAt  = "merged_at"
	fieldMissionID = "mission_id"
	fieldApprovers = "approvers"
	fieldGate      = "gate"
	fieldApprovedB = "approved_by"
	fieldApprovedA = "approved_at"
	fieldApprovedH = "approved_hash"
	gateHashShown  = 8 // how much of approved_hash messages print
	mergedShown    = "2006-01-02 15:04"
)

// ---- task reassign ----

// TaskReassign implements opscoord.Host: one task to --assign @handle
// (TASK 14.4 lead + assignees, 17.2 history, claim_epoch raised so the
// holder is fenced out). ops/coord sends --from through TaskReassignPlan
// and then one id at a time.
func (h standaloneCoord) TaskReassign(ctx context.Context, in opscoord.ReassignIn) (opscoord.Report, error) {
	w, to, err := h.reassignCaller(ctx, in)
	if err != nil {
		return opscoord.Report{}, err
	}
	if strings.TrimSpace(in.ID) == "" {
		return opscoord.Report{}, errs.New(errs.InvalidInput, "give a task id or --from @handle")
	}
	if err = w.forTask(ctx, in.ID); err != nil {
		return opscoord.Report{}, err
	}
	by := w.actorID
	if _, err = coord.OverrideChecked(ctx, w.env(ctx), in.ID, coord.OverrideReassign, func(d coord.Doc, now time.Time) error {
		prevLead, prevAll := d.Str(fieldTaskLead), d.Strs(schema.FieldTaskAssignees)
		d.SetStr(fieldTaskLead, to)
		d[schema.FieldTaskAssignees] = canonjson.Array(canonjson.String(to))
		return appendReassignHistory(d, by, now, prevLead, prevAll, to)
	}); err != nil {
		return opscoord.Report{}, coordErr(err)
	}
	return opscoord.Report{Lines: []string{markOK + " " + in.ID + " reassigned to " + in.Assign}}, nil
}

// TaskReassignPlan implements opscoord.Host: the open tasks (todo or in
// progress) assigned to --from, after the same checks; nothing written.
func (h standaloneCoord) TaskReassignPlan(ctx context.Context, in opscoord.ReassignIn) (opscoord.ReassignPlan, error) {
	w, _, err := h.reassignCaller(ctx, in)
	if err != nil {
		return opscoord.ReassignPlan{}, err
	}
	who, err := h.resolveFrom(ctx, in.From)
	if err != nil {
		return opscoord.ReassignPlan{}, err
	}
	docs, err := engineRows{a: h.a}.List(ctx)
	if err != nil {
		return opscoord.ReassignPlan{}, err
	}
	plan := opscoord.ReassignPlan{Tasks: []opscoord.ReassignTask{}, Store: store.SourceRepo, Actor: w.actorID}
	for _, d := range docs {
		s := d.Status()
		if d.Str(fieldTaskLead) == who && (s == schema.TaskStatusTodo || s == schema.TaskStatusInProgress) {
			plan.Tasks = append(plan.Tasks, opscoord.ReassignTask{ID: d.Str(docKeyID), Title: d.Str(fieldTitle)})
		}
	}
	return plan, nil
}

// reassignCaller checks --assign and that a person runs it, and resolves
// the new assignee's actor id on the repo roster.
func (h standaloneCoord) reassignCaller(ctx context.Context, in opscoord.ReassignIn) (*coordWorld, string, error) {
	if strings.TrimSpace(in.Assign) == "" {
		return nil, "", errs.New(errs.InvalidInput, "--assign is required: the new assignee").
			WithHint(`lore task reassign --id <id> --assign "@handle" (or --from "@handle" for every open task of a person)`)
	}
	w, err := h.openWorld(ctx)
	if err != nil {
		return nil, "", err
	}
	if err = w.requireHuman(ctx, "reassign"); err != nil {
		return nil, "", err
	}
	to, err := h.resolveActor(ctx, in.Assign)
	if err != nil {
		return nil, "", err
	}
	return w, to, nil
}

// resolveActor is @handle (a person on the repo roster) as an actor id; a
// bare actor id is accepted as is.
func (h standaloneCoord) resolveActor(ctx context.Context, handle string) (string, error) {
	v := strings.TrimSpace(handle)
	if id, err := ids.Parse[ids.Actor](v); err == nil {
		return id.String(), nil
	}
	as, err := standaloneTeam(h).ResolveHandle(ctx, v, false)
	if err != nil {
		return "", err
	}
	return as.ActorID, nil
}

// resolveFrom is --from as an actor id: an actor id as is, else the
// roster member @handle or email names, people who left included (B-12:
// the hand-over runs after `lore member remove`).
func (h standaloneCoord) resolveFrom(ctx context.Context, ref string) (string, error) {
	v := strings.TrimSpace(ref)
	if id, err := ids.Parse[ids.Actor](v); err == nil {
		return id.String(), nil
	}
	x, r, ok, err := h.a.rosterMember(ctx, v)
	switch {
	case err != nil:
		return "", err
	case ok:
		return x.ID, nil
	}
	if _, isRef := parseMemberRef(v); isRef {
		return "", memberMissErr(r, v)
	}
	return h.resolveActor(ctx, v)
}

// historyEntryTag types the ids of history entries reassign writes (the_…,
// TASK 17.2); entries are not rows, the id is the list's merge key.
type historyEntryTag struct{}

// Prefix implements ids.Tag.
func (historyEntryTag) Prefix() ids.Prefix { return ids.PrefixHistoryEntry }

// appendReassignHistory adds reassign's entries to d's history (TASK 17.2,
// OV59): one for the lead, one for the assignees (sorted, comma-joined, as
// the core writes them), each only when it changes. The cap
// (tasks.history_max) is folded by the next core write of the task.
func appendReassignHistory(d coord.Doc, by string, now time.Time, prevLead string, prevAll []string, to string) error {
	prev := slices.Clone(prevAll)
	slices.Sort(prev)
	at := now.UTC().Format(time.RFC3339)
	var add []opstask.HistoryEntry
	for _, c := range [][3]string{{fieldTaskLead, prevLead, to}, {schema.FieldTaskAssignees, strings.Join(prev, ","), to}} {
		if c[1] == c[2] {
			continue
		}
		id, err := ids.New[historyEntryTag]()
		if err != nil {
			return err
		}
		add = append(add, opstask.HistoryEntry{ID: id.String(), At: at, By: by, Field: c[0], From: c[1], To: c[2]})
	}
	if len(add) == 0 {
		return nil
	}
	var list []opstask.HistoryEntry
	if cur, ok := d[schema.FieldTaskHistory]; ok && !cur.IsNull() {
		s, err := canonjson.CompactValue(cur)
		if err != nil {
			return errs.Wrap(errs.Internal, "reassign: read history", err)
		}
		if err = json.Unmarshal([]byte(s), &list); err != nil {
			return errs.Wrap(errs.Internal, "reassign: read history", err)
		}
	}
	b, err := json.Marshal(append(list, add...))
	if err != nil {
		return errs.Wrap(errs.Internal, "reassign: encode history", err)
	}
	v, err := canonjson.Raw(b)
	if err != nil {
		return errs.Wrap(errs.Internal, "reassign: encode history", err)
	}
	d[schema.FieldTaskHistory] = v
	return nil
}

// ---- task merged ----

// TaskMerged implements opscoord.Host: record merged_at by hand on a done
// task whose merge lore cannot see (F9), through the fenced row writer.
func (h standaloneCoord) TaskMerged(ctx context.Context, in opscoord.MergedIn) (opscoord.Report, error) {
	if err := refuseFrom(opscoord.OpTaskMerged, in.From); err != nil {
		return opscoord.Report{}, err
	}
	ctx = mergedStampLive(ctx) // the same stamp by hand: live ref, then a landing
	w, err := h.openWorldFor(ctx, in.ID)
	if err != nil {
		return opscoord.Report{}, err
	}
	env := w.env(ctx)
	if d, ok, gerr := env.Rows.Get(ctx, in.ID); gerr == nil && ok && d.Status() == schema.TaskStatusDone {
		if prev := d.Time(fieldMergedAt); !prev.IsZero() {
			return opscoord.Report{Lines: []string{fmt.Sprintf("%s %s was already merged at %s", markOK, in.ID, prev.UTC().Format(mergedShown))}}, nil
		}
	}
	at := h.a.Now().UTC()
	if _, err = coord.Update(ctx, env, in.ID, func(d coord.Doc, _ time.Time) error {
		if d.Status() != schema.TaskStatusDone {
			return errs.New(errs.InvalidInput, fmt.Sprintf("%s is %s; only a done task can be merged", in.ID, d.Status()))
		}
		if d.Str(fieldMergedAt) == "" {
			d.SetTime(fieldMergedAt, at)
		}
		return nil
	}); err != nil {
		return opscoord.Report{}, coordErr(err)
	}
	return opscoord.Report{Lines: []string{fmt.Sprintf("%s %s merged (marked by hand, %s)", markOK, in.ID, at.Format(stateClock))}}, nil
}

// ---- hold claim / release ----

// Hold places (§7.2; CLI_REFERENCE 3.3 --on).
const (
	holdOnProject = "project"
	holdOnMachine = "machine"
)

// HoldClaim implements opscoord.Host (RES 3.17): hold a named resource on
// the project's signals ref, exclusively, until the ttl passes (default
// lease.ttl); claiming again renews. --on machine is not built.
func (h standaloneCoord) HoldClaim(ctx context.Context, in opscoord.HoldClaimIn) (opscoord.Report, error) {
	if in.On == holdOnMachine {
		return opscoord.Report{}, errs.New(errs.NotImplemented, "--on machine defers to a machine-wide lock; run the command under your machine's own lock tool (for example flock) instead")
	}
	if in.On != "" && in.On != holdOnProject {
		return opscoord.Report{}, errs.New(errs.InvalidInput, "--on is project or machine, not "+in.On)
	}
	w, err := h.openWorld(ctx)
	if err != nil {
		return opscoord.Report{}, err
	}
	if err = w.forHolds(ctx); err != nil {
		return opscoord.Report{}, err
	}
	st := w.signalsStore()
	d := in.TTL
	if d <= 0 {
		d = w.lease.TTL
	}
	var held signals.Signal
	_, err = st.Write(ctx, func(v signals.View) (signals.Change, error) {
		tok, terr := st.Tokens.New()
		if terr != nil {
			return signals.Change{}, terr
		}
		s, derr := signals.DecideHold(v, signals.HoldRequest{Caller: w.caller(), Folder: w.folder, Name: in.Name, TTL: d, Token: tok})
		if derr != nil {
			return signals.Change{}, derr
		}
		held = s
		return signals.Change{Put: map[string]signals.Signal{signals.ResourcePath(w.folder, in.Name): s}}, nil
	})
	if err != nil {
		return opscoord.Report{}, coordErr(err)
	}
	return opscoord.Report{Lines: []string{fmt.Sprintf("%s holding %s until %s", markOK, in.Name, held.Expires.UTC().Format(time.RFC3339))}}, nil
}

// HoldRelease implements opscoord.Host: remove your own hold.
func (h standaloneCoord) HoldRelease(ctx context.Context, in opscoord.HoldReleaseIn) (opscoord.Report, error) {
	w, err := h.openWorld(ctx)
	if err != nil {
		return opscoord.Report{}, err
	}
	if err = w.forHolds(ctx); err != nil {
		return opscoord.Report{}, err
	}
	_, err = w.signalsStore().Write(ctx, func(v signals.View) (signals.Change, error) {
		p, derr := signals.DecideUnhold(v, w.caller(), w.folder, in.Name)
		return signals.Change{Delete: []string{p}}, derr
	})
	if err != nil {
		return opscoord.Report{}, coordErr(err)
	}
	return opscoord.Report{Lines: []string{markOK + " released " + in.Name}}, nil
}

// ---- gate request / approve ----

// gateRequested is gate request's line.
func gateRequested(id string) []string {
	return []string{fmt.Sprintf("%s gate requested on %s; agents stop until a person runs `lore gate approve --id %s`", markOK, id, id)}
}

// GateRequest implements opscoord.Host (§7.4 "Gates", TEAM-028): the
// holder requests it through the fenced writer; anyone else stamps the
// row (a gate on a task nobody holds needs no fence).
func (h standaloneCoord) GateRequest(ctx context.Context, in opscoord.GateIn) (opscoord.Report, error) {
	if err := refuseFrom(opscoord.OpGateRequest, in.From); err != nil {
		return opscoord.Report{}, err
	}
	w, err := h.openWorldFor(ctx, in.ID)
	if err != nil {
		return opscoord.Report{}, err
	}
	env := w.env(ctx)
	v, err := env.Signals.Read(ctx)
	if err != nil {
		return opscoord.Report{}, coordErr(err)
	}
	p := signals.ClaimPath(w.folder, in.ID)
	sig, claimed := v.Board[p]
	_, hadClaim := w.held().Get(w.session(), in.ID)
	if (claimed && sig.Holder == w.actorID && sig.Session == w.session()) || hadClaim {
		if _, err = coord.HolderWrite(ctx, env, in.ID, false, coord.RequestGate); err != nil {
			return opscoord.Report{}, coordErr(err)
		}
		return opscoord.Report{Lines: gateRequested(in.ID)}, nil
	}
	if _, _, ok, gerr := h.a.locateRow(ctx, schema.TableTasks, in.ID); gerr != nil || !ok {
		if gerr != nil {
			return opscoord.Report{}, gerr
		}
		return opscoord.Report{}, errs.New(errs.NotFound, "no task "+in.ID).WithHint("`lore task list` shows the tasks here")
	}
	patch := coord.Doc{fieldGate: canonjson.String(schema.TaskGateRequested), fieldApprovedB: canonjson.Null(), fieldApprovedA: canonjson.Null(), fieldApprovedH: canonjson.Null()}
	if err = h.a.patchDoc(ctx, in.ID, patch); err != nil {
		return opscoord.Report{}, err
	}
	return opscoord.Report{Lines: gateRequested(in.ID)}, nil
}

// patchDoc writes only the fields patch names onto the task id.
func (a *App) patchDoc(ctx context.Context, id string, patch coord.Doc) error {
	_, cur, ok, err := a.locateRow(ctx, schema.TableTasks, id)
	if err != nil || !ok {
		return err
	}
	d, err := docOfRow(cur)
	if err != nil {
		return err
	}
	for k, v := range patch {
		d[k] = v
	}
	return a.writeDoc(ctx, schema.TableTasks, id, d, "")
}

// stepNames are the identity steps as v0.1.17's approve line named them.
var stepNames = map[identity.Step]string{
	identity.StepFlag: "flag", identity.StepEnv: "env", identity.StepLegacy: "identity.yaml",
	identity.StepGit: "git config", identity.StepOSUser: "OS user", identity.StepMachine: "machine-id",
	identity.StepSalt: "persisted-salt", identity.StepEphemeral: "ephemeral-salt",
}

// stepName is how the approve line names the step that answered.
func stepName(s identity.Step) string {
	if n, ok := stepNames[s]; ok {
		return n
	}
	return string(s)
}

// GateApprove implements opscoord.Host: only a person from the identity
// file or config approves (never LORE_ACTOR), a mission's approvers list
// is honoured, and approved_hash covers title + body.
func (h standaloneCoord) GateApprove(ctx context.Context, in opscoord.GateIn) (opscoord.Report, error) {
	if err := refuseFrom(opscoord.OpGateApprove, in.From); err != nil {
		return opscoord.Report{}, err
	}
	if v := strings.TrimSpace(os.Getenv(identity.EnvActor)); v != "" {
		return opscoord.Report{}, errs.New(errs.InvalidInput, fmt.Sprintf("approvals come from your identity file, never from %s (%s is set here)", identity.EnvActor, identity.EnvActor)).
			WithHint("an agent session cannot approve; run `lore gate approve` in your own shell")
	}
	w, err := h.openWorldFor(ctx, in.ID)
	if err != nil {
		return opscoord.Report{}, err
	}
	// A person approves, from their identity file or config. A detected
	// coding agent approves as the person it acts for (owner rule
	// 2026-11-11; the output says "<person> via <tool>"); a team keeps
	// approvals to people with `agents.person_only gate.approve`.
	person := w.who
	if w.who.Kind == identity.KindAgent && w.who.Owner != nil {
		person = *w.who.Owner
	}
	if person.Kind != identity.KindHuman || person.Step == identity.StepEnv || person.Step == identity.StepFlag {
		return opscoord.Report{}, errs.New(errs.InvalidInput, "only a person approves a gate (TEAM-028); you are "+w.who.StableKey).
			WithHint("set your identity with `lore identity set` and approve from your own shell")
	}
	approver := w.personID()
	if err = h.checkApprover(ctx, in.ID, approver); err != nil {
		return opscoord.Report{}, err
	}
	ap, err := coord.Approve(ctx, w.env(ctx), coord.ApproveRequest{ID: in.ID, ApproverID: approver})
	if err != nil {
		return opscoord.Report{}, coordErr(err)
	}
	return opscoord.Report{Lines: []string{fmt.Sprintf("%s %s approved by %s (%s), hash %s…", markOK, in.ID, w.who.DisplayName, stepName(w.who.Step), ap.Hash[:min(len(ap.Hash), gateHashShown)])}}, nil
}

// checkApprover enforces the mission's approvers list, when it has one.
// It fails closed: a task or mission that cannot be read refuses the
// approval (the list may name someone else); only a task with no mission,
// or a mission row that does not exist (no list to check), passes.
func (h standaloneCoord) checkApprover(ctx context.Context, id, actorID string) error {
	rows := engineRows{a: h.a}
	task, ok, err := rows.Get(ctx, id)
	if err != nil {
		return errs.Prefix("gate approve: read "+id+" to check its mission's approvers", err)
	}
	if !ok || task.Str(fieldMissionID) == "" {
		return nil // no mission: no list to check (an unknown id is refused by the approval itself)
	}
	m, ok, err := rows.Get(ctx, task.Str(fieldMissionID))
	if err != nil {
		return errs.Prefix("gate approve: read mission "+task.Str(fieldMissionID)+" to check its approvers", err)
	}
	if !ok {
		return nil
	}
	approvers := m.Strs(fieldApprovers)
	if len(approvers) == 0 {
		return nil
	}
	if !slices.Contains(approvers, actorID) {
		return errs.New(errs.InvalidInput, fmt.Sprintf("mission %s names its approvers; you are not one of them", m.Str(docKeyID))).
			WithHint("ask one of: " + strings.Join(approvers, ", "))
	}
	return nil
}
