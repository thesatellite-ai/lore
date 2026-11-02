package lsync

import (
	"context"
	"reflect"
	"testing"
	"time"

	"dbent/gen/ent"
	enttask "dbent/gen/ent/task"
	"dbent/schema"
)

// The lore-for-teams columns (LORE_TEAM_SPEC §10 C14–C16) travel through
// `.lore/data` like every other synced column. These tests prove it the way
// a teammate experiences it: rows written on one machine are exported,
// copied to a fresh clone (another cache), imported, and read back through
// ent with every value intact — including the typed JSON columns, which are
// the ones most likely to be mangled by a codec (numbers, nested objects,
// timestamps inside JSON).

// teamTaskFixture holds the values written on machine A and expected on B.
type teamTaskFixture struct {
	deps      []schema.TaskDependency
	log       []schema.ClaimLogEntry
	approved  time.Time
	merged    time.Time
	timebox   int64
	pr        int
	epoch     int
	attempts  int
	approvers []string
}

func newTeamTaskFixture() teamTaskFixture {
	at := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)
	return teamTaskFixture{
		deps: []schema.TaskDependency{
			{TaskID: "tsk_01a10237c9e87e4c843d8045973a65b2", Until: schema.DependencyUntilDone},
			{TaskID: "tsk_01a10237c9e87e4c843d8045973a65b3", ProjectID: "prj_01a10237c9e87e4c843d8045973a65b4", Until: schema.DependencyUntilMerged},
		},
		log: []schema.ClaimLogEntry{
			{At: at, Event: schema.ClaimEventClaim, ActorID: "act_a", Session: "s1", Epoch: 1},
			{At: at.Add(30 * time.Minute), Event: schema.ClaimEventReclaim, ActorID: "act_b", Session: "s2", PrevActorID: "act_a", Epoch: 2},
		},
		approved:  at.Add(time.Hour),
		merged:    at.Add(2 * time.Hour),
		timebox:   3600,
		pr:        42,
		epoch:     2,
		attempts:  1,
		approvers: []string{"act_x", "act_y"},
	}
}

func TestTeamFields_RoundTripThroughFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newEnv(t)
	f := newTeamTaskFixture()

	mis, err := a.client.Mission.Create().SetProjectID(a.project).SetTitle("m").
		SetApprovers(f.approvers).SetIdempotencyKey("invite").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	team, err := a.client.Team.Create().SetProjectID(a.project).SetHandle("frontend").
		SetDisplayName("Frontend").SetMemberActorIds([]string{a.actor}).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tsk, err := a.client.Task.Create().SetProjectID(a.project).SetTitle("invite route").
		SetMissionID(mis.ID).SetAssignedTeam(team.ID).SetBlockedBy(f.deps).
		SetClaimEpoch(f.epoch).SetAttempts(f.attempts).SetClaimLog(f.log).
		SetTimeboxSeconds(f.timebox).SetGate(enttask.Gate(schema.TaskGateApproved)).
		SetApprovedBy(a.actor).SetApprovedAt(f.approved).SetApprovedHash("h1").
		SetDoneBy(a.actor).SetDoneCommit("abc123").SetDonePr(f.pr).SetMergedAt(f.merged).
		SetWorkBranch("agent/frontend-2/invite").SetIdempotencyKey("frontend").SetRole("coordinator").
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := a.client.Actor.Create().SetKind("agent").SetDisplayName("frontend-2").
		SetStableKey("agent:frontend-2").SetOwnerActorID(a.actor).SetLabel("frontend-2").
		SetHandle("fe2").SetActive(false).SetLeftAt(f.merged).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cmt, err := a.client.Comment.Create().SetEntityTable("tasks").SetEntityID(tsk.ID).
		SetEntityProjectID("prj_01a10237c9e87e4c843d8045973a65b4").SetBody("note").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := a.client.KnowledgeRef.Create().SetSrcTable("tasks").SetSrcID(tsk.ID).
		SetDstTable("tasks").SetDstID(f.deps[1].TaskID).SetDstProjectID(f.deps[1].ProjectID).
		SetRelation("mentions").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hof, err := a.client.Handoff.Create().SetProjectID(a.project).SetFromActorID(a.actor).
		SetToActorID(agent.ID).SetBody("schema is merged; take the UI").SetStatusStr("open").
		SetTaskID(tsk.ID).SetMissionID(mis.ID).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.reconcile()

	// A fresh clone imports the files.
	b := newEmptyEnv(t)
	a.copyDataTo(b)
	b.reconcile()

	gotTask, err := b.client.Task.Get(ctx, tsk.ID)
	if err != nil {
		t.Fatalf("task not imported: %v", err)
	}
	if !reflect.DeepEqual(gotTask.BlockedBy, f.deps) {
		t.Errorf("blocked_by = %+v, want %+v", gotTask.BlockedBy, f.deps)
	}
	if !reflect.DeepEqual(gotTask.ClaimLog, f.log) {
		t.Errorf("claim_log = %+v, want %+v", gotTask.ClaimLog, f.log)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"assigned_team", deref(gotTask.AssignedTeam), team.ID},
		{"claim_epoch", gotTask.ClaimEpoch, f.epoch},
		{"attempts", gotTask.Attempts, f.attempts},
		{"timebox_seconds", deref(gotTask.TimeboxSeconds), f.timebox},
		{"gate", deref(gotTask.Gate), enttask.Gate(schema.TaskGateApproved)},
		{"approved_by", deref(gotTask.ApprovedBy), a.actor},
		{"approved_at", derefTime(gotTask.ApprovedAt), f.approved},
		{"approved_hash", deref(gotTask.ApprovedHash), "h1"},
		{"done_by", deref(gotTask.DoneBy), a.actor},
		{"done_commit", deref(gotTask.DoneCommit), "abc123"},
		{"done_pr", deref(gotTask.DonePr), f.pr},
		{"merged_at", derefTime(gotTask.MergedAt), f.merged},
		{"work_branch", deref(gotTask.WorkBranch), "agent/frontend-2/invite"},
		{"idempotency_key", deref(gotTask.IdempotencyKey), "frontend"},
		{"role", deref(gotTask.Role), "coordinator"},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("task.%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	gotMis, err := b.client.Mission.Get(ctx, mis.ID)
	if err != nil || !reflect.DeepEqual(gotMis.Approvers, f.approvers) {
		t.Errorf("mission approvers = %v (%v), want %v", gotMis.Approvers, err, f.approvers)
	}
	if err == nil && (gotMis.IdempotencyKey == nil || *gotMis.IdempotencyKey != "invite") {
		t.Errorf("mission idempotency_key = %v, want invite", gotMis.IdempotencyKey)
	}
	gotTeam, err := b.client.Team.Get(ctx, team.ID)
	if err != nil || gotTeam.Handle != "frontend" || !reflect.DeepEqual(gotTeam.MemberActorIds, []string{a.actor}) || !gotTeam.Active {
		t.Errorf("team = %+v (%v)", gotTeam, err)
	}
	gotAgent, err := b.client.Actor.Get(ctx, agent.ID)
	if err != nil || deref(gotAgent.OwnerActorID) != a.actor || deref(gotAgent.Label) != "frontend-2" ||
		deref(gotAgent.Handle) != "fe2" || gotAgent.Active || !derefTime(gotAgent.LeftAt).Equal(f.merged) {
		t.Errorf("agent actor = %+v (%v)", gotAgent, err)
	}
	gotCmt, err := b.client.Comment.Get(ctx, cmt.ID)
	if err != nil || deref(gotCmt.EntityProjectID) != "prj_01a10237c9e87e4c843d8045973a65b4" {
		t.Errorf("comment entity_project_id = %+v (%v)", gotCmt, err)
	}
	gotHof, err := b.client.Handoff.Get(ctx, hof.ID)
	if err != nil || deref(gotHof.TaskID) != tsk.ID || deref(gotHof.MissionID) != mis.ID {
		t.Errorf("handoff task_id/mission_id = %+v (%v), want %s/%s", gotHof, err, tsk.ID, mis.ID)
	}
	gotRef, err := b.client.KnowledgeRef.Get(ctx, ref.ID)
	if err != nil || deref(gotRef.DstProjectID) != f.deps[1].ProjectID {
		t.Errorf("knowledge ref dst_project_id = %+v (%v)", gotRef, err)
	}
}

// A row file written before the team feature has none of the new keys. It
// must import with the documented "feature unused" values: no gate, epoch
// 0, no dependencies, actor active. Anything else would silently gate or
// fence every old task the first time a teammate upgrades.
func TestTeamFields_PreTeamFilesImportWithNeutralDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newEnv(t)
	tsk, err := a.client.Task.Create().SetProjectID(a.project).SetTitle("old task").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.reconcile()

	newKeys := map[string][]string{
		"tasks": {"assigned_team", "blocked_by", "claim_epoch", "attempts", "claim_log", "timebox_seconds",
			"gate", "approved_by", "approved_at", "approved_hash", "done_by", "done_commit", "done_pr",
			"merged_at", "work_branch", "idempotency_key", "role"},
		"actors": {"owner_actor_id", "label", "handle", "active", "left_at"},
	}
	ids := map[string]string{"tasks": tsk.ID, "actors": a.actor}
	for table, keys := range newKeys {
		a.editFile(table, ids[table], func(d map[string]any) {
			for _, k := range keys {
				if _, ok := d[k]; !ok {
					t.Fatalf("%s export lacks new key %q; the test would prove nothing", table, k)
				}
				delete(d, k)
			}
		})
	}

	b := newEmptyEnv(t)
	a.copyDataTo(b)
	b.reconcile()

	got, err := b.client.Task.Get(ctx, tsk.ID)
	if err != nil {
		t.Fatalf("old task not imported: %v", err)
	}
	if got.Gate != nil || got.ClaimEpoch != 0 || got.Attempts != 0 || len(got.BlockedBy) != 0 ||
		len(got.ClaimLog) != 0 || got.MergedAt != nil || got.AssignedTeam != nil {
		t.Fatalf("old task imported with non-neutral team fields: %+v", got)
	}
	act, err := b.client.Actor.Get(ctx, a.actor)
	if err != nil || !act.Active || act.Handle != nil || act.OwnerActorID != nil {
		t.Fatalf("old actor imported with non-neutral roster fields: %+v (%v)", act, err)
	}
}

// Two machines adding the same team derive the same id (natural key
// project_id + handle), so the roster ends with one file, not two.
func TestTeamFields_TeamHandleUniquePerProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	if _, err := e.client.Team.Create().SetProjectID(e.project).SetHandle("ops").Save(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := e.client.Team.Create().SetProjectID(e.project).SetHandle("ops").Save(ctx)
	if !ent.IsConstraintError(err) {
		t.Fatalf("second team with the same handle in one project: err = %v, want a constraint error", err)
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func derefTime(p *time.Time) time.Time { return deref(p).UTC() }
