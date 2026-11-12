package period

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/ops/profilestore/profilestoretest"
	"github.com/thesatellite-ai/lore/schema"
)

// now is the fake's wall clock: 2026-09-30 15:00 UTC.
var now = time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)

// testID is a valid id of prefix p numbered n.
func testID(t *testing.T, p ids.Prefix, n int) ids.AnyID {
	t.Helper()
	id, err := ids.ParseAny(fmt.Sprintf("%s_%012x70009%015x", p, 0x01890a5d8a2b, n))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func txt(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func date(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.DateOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func ref(t *testing.T, id ids.AnyID) value.Value {
	t.Helper()
	v, err := value.RefOf(id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// world is a roster with Mira (human, @mira, joined 2026-01-12), her agent,
// Sam (the caller), two projects and Mira's two open memberships.
type world struct {
	f            *profilestoretest.Fake
	mira, agent  ids.AnyID
	sam          ids.AnyID
	hub, nw      ids.AnyID
	hubM, nwM    ids.AnyID
	tasks        []OpenTask
	tasksErr     error
	withoutTasks bool
}

// env is the fake plus the TaskReader upgrade.
type env struct {
	*profilestoretest.Fake
	w *world
}

func (e env) OpenTasks(_ context.Context, _ ids.AnyID) ([]OpenTask, error) {
	return e.w.tasks, e.w.tasksErr
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{mira: testID(t, ids.PrefixActor, 1), agent: testID(t, ids.PrefixActor, 2), sam: testID(t, ids.PrefixActor, 3),
		hub: testID(t, ids.PrefixProject, 4), nw: testID(t, ids.PrefixProject, 5)}
	caller, err := ids.Parse[ids.Actor](w.sam.String())
	if err != nil {
		t.Fatal(err)
	}
	w.f = profilestoretest.New(caller, now)
	joined := time.Date(2026, 1, 12, 9, 0, 0, 0, time.UTC)
	w.f.Put(row.Row{Table: schema.TableActors, ID: w.mira, CreatedAt: joined, UpdatedAt: joined, Fields: map[string]value.Value{
		"kind": txt(t, schema.ActorKindHuman), "display_name": txt(t, "Mira Patel"), "handle": txt(t, "mira"),
		"stable_key": txt(t, "human:mira@acme.com"), "active": value.BoolOf(true)}})
	w.f.Put(row.Row{Table: schema.TableActors, ID: w.agent, CreatedAt: joined, Fields: map[string]value.Value{
		"kind": txt(t, schema.ActorKindAgent), "display_name": txt(t, "codex"), "stable_key": txt(t, "agent:auto/codex/3f9a1c"),
		"owner_actor_id": ref(t, w.mira), "active": value.BoolOf(true)}})
	w.f.Put(row.Row{Table: schema.TableActors, ID: w.sam, CreatedAt: joined, Fields: map[string]value.Value{
		"kind": txt(t, schema.ActorKindHuman), "display_name": txt(t, "Sam Rivera"), "handle": txt(t, "sam"), "active": value.BoolOf(true)}})
	w.f.Put(row.Row{Table: schema.TableProjects, ID: w.hub, Fields: map[string]value.Value{"name": txt(t, "acme-hub")}})
	w.f.Put(row.Row{Table: schema.TableProjects, ID: w.nw, Fields: map[string]value.Value{"name": txt(t, "northwind-app")}})
	w.hubM = w.member(t, w.hub, "2026-01-12", "dev", "reviewer")
	w.nwM = w.member(t, w.nw, "2026-03-01", "lead")
	w.tasks = []OpenTask{{ID: "tsk_01aa", Title: "Rotate staging keys", Store: "repo", How: TaskOnAssignee}}
	return w
}

// member seeds an open membership of Mira.
func (w *world) member(t *testing.T, project ids.AnyID, started string, roles ...string) ids.AnyID {
	t.Helper()
	items := make([]value.Value, 0, len(roles))
	for _, r := range roles {
		items = append(items, txt(t, r))
	}
	l, err := value.ListOf(value.KindText, items...)
	if err != nil {
		t.Fatal(err)
	}
	id := testID(t, ids.PrefixProjectMember, len(w.f.All(schema.TableProjectMembers))+10)
	w.f.Put(row.Row{Table: schema.TableProjectMembers, ID: id, Fields: map[string]value.Value{
		schema.FieldMemberProject: ref(t, project), schema.FieldMemberActor: ref(t, w.mira),
		schema.FieldMemberRoles: l, schema.FieldMemberStartedOn: date(t, started)}})
	return id
}

func (w *world) ops() ops {
	if w.withoutTasks {
		return ops{env: w.f}
	}
	return ops{env: env{Fake: w.f, w: w}}
}

func (w *world) get(t *testing.T, table row.Table, id ids.AnyID) row.Row {
	t.Helper()
	for _, r := range w.f.All(table) {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no %s row %s", table, id)
	return row.Row{}
}

// snapshot is every row of the profile tables and actors, for "nothing
// was written" checks.
func (w *world) snapshot() map[row.Table][]row.Row {
	out := map[row.Table][]row.Row{}
	for _, tb := range []row.Table{schema.TableActors, schema.TableActorPeriods, schema.TableProjectMembers} {
		out[tb] = w.f.All(tb)
	}
	return out
}

func yes() context.Context { return op.WithConfirmed(context.Background(), true) }

func wantCode(t *testing.T, err error, code errs.Code) {
	t.Helper()
	if !errs.Has(err, code) {
		t.Fatalf("want %s, got %v", code, err)
	}
}

// TestStatusMoveMatrix pins TEAM_PROFILES 5.3 for people, every from × to,
// and that a non-human moves freely.
func TestStatusMoveMatrix(t *testing.T) {
	const (
		a, l, g, x = schema.ActorStatusActive, schema.ActorStatusOnLeave, schema.ActorStatusLeft, schema.ActorStatusArchived
	)
	type want struct {
		mv   move
		code errs.Code
	}
	w := func(c errs.Code) want { return want{code: c} }
	ok := want{mv: moveWrite}
	noop := want{mv: moveNoop}
	table := map[[2]string]want{
		{a, a}: noop, {a, l}: ok, {a, g}: w(errs.StatusChange), {a, x}: w(errs.StatusChange),
		{l, a}: ok, {l, l}: ok, {l, g}: w(errs.StatusChange), {l, x}: w(errs.StatusChange),
		{g, a}: w(errs.StatusChange), {g, l}: w(errs.StatusChange), {g, g}: noop, {g, x}: ok,
		{x, a}: w(errs.StatusChange), {x, l}: w(errs.StatusChange), {x, g}: ok, {x, x}: noop,
	}
	for k, want := range table {
		mv, err := statusMove(true, k[0], k[1])
		if want.code != "" {
			if !errs.Has(err, want.code) {
				t.Errorf("%s → %s: want %s, got %v", k[0], k[1], want.code, err)
			}
			continue
		}
		if err != nil || mv != want.mv {
			t.Errorf("%s → %s: want %v, got %v %v", k[0], k[1], want.mv, mv, err)
		}
		// agents: everything that is not a no-op writes
		if amv, aerr := statusMove(false, k[0], k[1]); aerr != nil || (k[0] != k[1] && amv != moveWrite) {
			t.Errorf("agent %s → %s: %v %v", k[0], k[1], amv, aerr)
		}
	}
	for _, bad := range []string{"", "gone", "Active"} {
		if _, err := statusMove(true, a, bad); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("to %q: %v", bad, err)
		}
	}
}

// TestStatusOnLeaveAndBack: on_leave sets the leave dates and keeps active
// true; active clears them; a no-op writes nothing; refusals name the
// command.
func TestStatusOnLeaveAndBack(t *testing.T) {
	w := newWorld(t)
	o := w.ops()
	ctx := context.Background()
	out, err := o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusOnLeave, From: "2026-06-01", Until: "2026-06-30"})
	if err != nil || !out.Changed {
		t.Fatalf("on_leave: %+v %v", out, err)
	}
	m := w.get(t, schema.TableActors, w.mira)
	if text(m, schema.FieldActorStatus) != schema.ActorStatusOnLeave || text(m, schema.FieldActorLeaveFrom) != "2026-06-01" ||
		text(m, schema.FieldActorLeaveUntil) != "2026-06-30" || text(m, schema.FieldActorActive) != "true" {
		t.Fatalf("on_leave row: %v", m.Fields)
	}
	if !strings.Contains(out.Text(), "active → on_leave (2026-06-01 → 2026-06-30)") {
		t.Errorf("text: %q", out.Text())
	}
	if _, err = o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusOnLeave, From: "2026-06-10", Until: "2026-06-01"}); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("until before from: %v", err)
	}
	if _, err = o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusActive}); err != nil {
		t.Fatal(err)
	}
	m = w.get(t, schema.TableActors, w.mira)
	if _, has := m.Fields[schema.FieldActorLeaveFrom]; has || text(m, schema.FieldActorStatus) != schema.ActorStatusActive {
		t.Fatalf("back: %v", m.Fields)
	}
	before := w.snapshot()
	pushes := w.f.Pushes
	out, err = o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusActive})
	if err != nil || out.Changed || !reflect.DeepEqual(before, w.snapshot()) || w.f.Pushes != pushes {
		t.Fatalf("no-op wrote: %+v %v", out, err)
	}
	_, err = o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusLeft})
	wantCode(t, err, errs.StatusChange)
	if e := err.(*errs.Error); !strings.Contains(e.Hint, "lore actor offboard @mira") {
		t.Errorf("hint: %q", e.Hint)
	}
	_, err = o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusActive, From: "2026-06-01"})
	wantCode(t, err, errs.InvalidInput)
	w.f.Agent = true
	_, err = o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusOnLeave})
	wantCode(t, err, errs.PolicyDenied)
}

// TestStatusNote (TEAM_PROFILES 3.4, 5.3): --note is kept on the person
// while they are not active, across a later status write without it; a
// note-only change on an unchanged status writes only the note; going
// active (status or rejoin) clears it; --note with active is refused; a
// row without the field reads as no note.
func TestStatusNote(t *testing.T) {
	w := newWorld(t)
	o := w.ops()
	ctx := context.Background()
	note := func() string { return text(w.get(t, schema.TableActors, w.mira), schema.FieldActorStatusNote) }
	if note() != "" {
		t.Fatal("seed row has a note")
	}
	_, err := o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusActive, Note: "back"})
	wantCode(t, err, errs.InvalidInput)
	if e := err.(*errs.Error); !strings.Contains(e.Hint, "on_leave, left or archived") {
		t.Errorf("hint: %q", e.Hint)
	}
	out, err := o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusOnLeave, From: "2026-06-01", Note: "parental leave"})
	if err != nil || out.Note != "parental leave" || note() != "parental leave" {
		t.Fatalf("on_leave --note: %+v %v (row %q)", out, err, note())
	}
	if !strings.Contains(out.Text(), "active → on_leave (2026-06-01 → open-ended)\n  note: parental leave\n") {
		t.Errorf("text: %q", out.Text())
	}
	// A later on_leave write without --note keeps it.
	if out, err = o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusOnLeave, From: "2026-06-01", Until: "2026-08-31"}); err != nil || out.Note != "parental leave" || note() != "parental leave" {
		t.Fatalf("update dates: %+v %v (row %q)", out, err, note())
	}
	if out, err = o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusActive}); err != nil || out.Note != "" || note() != "" {
		t.Fatalf("active did not clear the note: %+v %v (row %q)", out, err, note())
	}
	// left → left --note: only the note is written.
	if _, err = o.offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-09-01"}); err != nil {
		t.Fatal(err)
	}
	before := text(w.get(t, schema.TableActors, w.mira), schema.FieldActorLeftAt)
	out, err = o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusLeft, Note: "moved abroad"})
	if err != nil || !out.Changed || note() != "moved abroad" || text(w.get(t, schema.TableActors, w.mira), schema.FieldActorLeftAt) != before {
		t.Fatalf("left --note: %+v %v", out, err)
	}
	if !strings.Contains(out.Text(), "is already left; note updated\n  note: moved abroad\n") {
		t.Errorf("text: %q", out.Text())
	}
	pushes := w.f.Pushes
	if out, err = o.status(ctx, ActorStatusIn{Actor: "@mira", To: schema.ActorStatusLeft, Note: "moved abroad"}); err != nil || out.Changed || w.f.Pushes != pushes {
		t.Fatalf("same note again wrote: %+v %v", out, err)
	}
	if _, err = o.rejoin(yes(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor, On: "2026-09-15"}); err != nil {
		t.Fatal(err)
	}
	if note() != "" {
		t.Errorf("rejoin kept the note %q", note())
	}
}

// TestOffboardWithoutYesWritesNothing: no confirm, E_NEEDS_CONFIRM with
// the preview as the question, and not one row changed (TP-20).
func TestOffboardWithoutYesWritesNothing(t *testing.T) {
	w := newWorld(t)
	before := w.snapshot()
	_, err := w.ops().offboard(context.Background(), OffboardIn{Actor: "@mira", On: "2026-09-30", Reason: "moved abroad"})
	wantCode(t, err, errs.NeedsConfirm)
	if !reflect.DeepEqual(before, w.snapshot()) || w.f.Pushes != 0 {
		t.Fatal("offboard without --yes wrote")
	}
	q := err.(*errs.Error).Question
	for _, want := range []string{"offboard Mira Patel (@mira)", "2026-01-12 → 2026-09-30  employee", "reason: moved abroad",
		"acme-hub (dev, reviewer) ends 2026-09-30", "northwind-app (lead) ends 2026-09-30",
		"tsk_01aa", "reassign: lore task edit tsk_01aa --assign @<someone>", "agent:auto/codex/3f9a1c", "becomes inactive", "active → left"} {
		if !strings.Contains(q, want) {
			t.Errorf("preview misses %q:\n%s", want, q)
		}
	}
}

// TestOffboardApplies: with --yes every open membership ends on the same
// day (TP-16), the owned agent becomes inactive (TP-18), the read-time
// period is materialised and ended, the actor is left with active false,
// tasks are never touched (TP-17), and a second run is "already left".
func TestOffboardApplies(t *testing.T) {
	w := newWorld(t)
	o := w.ops()
	out, err := o.offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-09-30", Reason: "moved abroad"})
	if err != nil || !out.Applied {
		t.Fatalf("offboard: %+v %v", out, err)
	}
	for _, id := range []ids.AnyID{w.hubM, w.nwM} {
		m := w.get(t, schema.TableProjectMembers, id)
		if text(m, schema.FieldMemberEndedOn) != "2026-09-30" || text(m, schema.FieldMemberEndReason) != schema.MemberEndReasonOffboarded {
			t.Errorf("membership %s: %v", id, m.Fields)
		}
	}
	g := w.get(t, schema.TableActors, w.agent)
	if text(g, schema.FieldActorActive) != "false" || text(g, schema.FieldActorStatus) != schema.ActorStatusLeft || text(g, schema.FieldActorLeftAt) == "" {
		t.Errorf("agent: %v", g.Fields)
	}
	ps := w.f.All(schema.TableActorPeriods)
	if len(ps) != 1 || text(ps[0], schema.FieldPeriodStartedOn) != "2026-01-12" || text(ps[0], schema.FieldPeriodEndedOn) != "2026-09-30" ||
		text(ps[0], schema.FieldPeriodEndReason) != "moved abroad" || text(ps[0], schema.FieldPeriodSource) != schema.PeriodSourceMigrated {
		t.Fatalf("period: %v", ps)
	}
	m := w.get(t, schema.TableActors, w.mira)
	if text(m, schema.FieldActorStatus) != schema.ActorStatusLeft || text(m, schema.FieldActorActive) != "false" || !strings.HasPrefix(text(m, schema.FieldActorLeftAt), "2026-09-30") {
		t.Fatalf("actor: %v", m.Fields)
	}
	if w.f.Pushes != 1 {
		t.Errorf("pushes %d", w.f.Pushes)
	}
	if !strings.Contains(out.Text(), "offboarded on 2026-09-30 (2 memberships ended, 1 agents inactive, 1 open tasks listed above)") {
		t.Errorf("text: %s", out.Text())
	}
	_, err = o.offboard(yes(), OffboardIn{Actor: "@mira"})
	wantCode(t, err, errs.StatusChange)
	if !strings.Contains(err.Error(), "already left on 2026-09-30") {
		t.Errorf("second run: %v", err)
	}
}

// TestOffboardRefusals: input matrix of the refusals before any write.
func TestOffboardRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		in   OffboardIn
		set  func(*world)
		code errs.Code
	}{
		"future --on":           {in: OffboardIn{Actor: "@mira", On: "2026-10-01"}, code: errs.InvalidInput},
		"bad --on":              {in: OffboardIn{Actor: "@mira", On: "30/09/2026"}, code: errs.InvalidInput},
		"before the stint":      {in: OffboardIn{Actor: "@mira", On: "2026-01-11"}, code: errs.PeriodOverlap},
		"before the membership": {in: OffboardIn{Actor: "@mira", On: "2026-02-01"}, code: errs.PeriodOverlap},
		"an agent":              {in: OffboardIn{Actor: "agent:auto/codex/3f9a1c"}, code: errs.InvalidInput},
		"unknown":               {in: OffboardIn{Actor: "@nobody"}, code: errs.NotFound},
		"agent caller":          {in: OffboardIn{Actor: "@mira"}, set: func(w *world) { w.f.Agent = true }, code: errs.PolicyDenied},
		"no org":                {in: OffboardIn{Actor: "@mira"}, set: func(w *world) { w.f.NoOrg = true }, code: errs.Refused},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			if tc.set != nil {
				tc.set(w)
			}
			before := w.snapshot()
			_, err := w.ops().offboard(yes(), tc.in)
			wantCode(t, err, tc.code)
			if !reflect.DeepEqual(before, w.snapshot()) {
				t.Fatal("a refused offboard wrote")
			}
		})
	}
	// A policy that allows agents lets the agent caller through.
	w := newWorld(t)
	w.f.Agent, w.f.MayManagePeople = true, true
	if _, err := w.ops().offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-09-01"}); err != nil {
		t.Fatalf("agents.may_manage_people: %v", err)
	}
}

// TestOffboardTasksNote: a host without the upgrade, or one that cannot
// read, says so in the preview instead of failing.
func TestOffboardTasksNote(t *testing.T) {
	w := newWorld(t)
	w.withoutTasks = true
	out, err := w.ops().offboard(yes(), OffboardIn{Actor: "@mira", Archive: true})
	if err != nil || !strings.Contains(out.Text(), "not listed by this surface") || out.To != schema.ActorStatusArchived {
		t.Fatalf("%+v %v", out, err)
	}
	w = newWorld(t)
	w.tasksErr = errs.New(errs.NotFound, "no repo here")
	out, err = w.ops().offboard(yes(), OffboardIn{Actor: "@mira"})
	if err != nil || !strings.Contains(out.TasksNote, "no repo here") {
		t.Fatalf("%+v %v", out, err)
	}
}

// TestOffboardClaimsUnread: a host that read the tasks but not the claim
// holders lists the tasks it read and says the claims are missing; a
// claim-held task prints its "claim" kind with the reassign line.
func TestOffboardClaimsUnread(t *testing.T) {
	w := newWorld(t)
	read := []OpenTask{{ID: "tsk_01bb", Title: "Fix login", Store: "repo", How: TaskOnClaim}}
	w.tasksErr = &ClaimsUnreadError{Tasks: read, Reason: "no signals ref"}
	out, err := w.ops().offboard(yes(), OffboardIn{Actor: "@mira"})
	if err != nil {
		t.Fatal(err)
	}
	text := out.Text()
	if len(out.OpenTasks) != 1 || out.OpenTasks[0] != read[0] ||
		!strings.Contains(out.TasksNote, "claim holders could not be read here (no signals ref)") ||
		!strings.Contains(text, "tsk_01bb  \"Fix login\"  repo  claim  reassign: lore task edit tsk_01bb --assign @<someone>") {
		t.Fatalf("%+v\n%s", out, text)
	}
}

// TestRejoin: a new period on the same actor, memberships not restored
// (listed), the agent stays inactive, status active; refusals.
func TestRejoin(t *testing.T) {
	w := newWorld(t)
	o := w.ops()
	ctx := context.Background()
	_, err := o.rejoin(yes(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor})
	wantCode(t, err, errs.StatusChange) // still here
	if _, err = o.offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-06-30"}); err != nil {
		t.Fatal(err)
	}
	before := w.snapshot()
	_, err = o.rejoin(ctx, RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor, On: "2026-09-01", OrgRole: "Contract engineer"})
	wantCode(t, err, errs.NeedsConfirm)
	q := err.(*errs.Error).Question
	for _, want := range []string{"lore project member add acme-hub", "--role dev,reviewer", "memberships are not restored", "stays inactive"} {
		if !strings.Contains(q, want) {
			t.Errorf("preview misses %q:\n%s", want, q)
		}
	}
	if !reflect.DeepEqual(before, w.snapshot()) {
		t.Fatal("rejoin without --yes wrote")
	}
	_, err = o.rejoin(yes(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor, On: "2026-06-30"})
	wantCode(t, err, errs.PeriodOverlap)
	_, err = o.rejoin(yes(), RejoinIn{Actor: "@mira", Type: "boss"})
	wantCode(t, err, errs.InvalidInput)
	// 5.2: a future --on is refused (no scheduling in 0.1.18); the hint
	// names today and past days as the way forward.
	_, err = o.rejoin(yes(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor, On: "2026-10-01"})
	wantCode(t, err, errs.InvalidInput)
	if e := err.(*errs.Error); !strings.Contains(e.Message, "2026-10-01 is in the future") || !strings.Contains(e.Hint, "--on 2026-09-30 (today) or a past day") {
		t.Errorf("future --on: %q / %q", e.Message, e.Hint)
	}
	if !reflect.DeepEqual(before, w.snapshot()) {
		t.Fatal("a refused rejoin wrote")
	}
	out, err := o.rejoin(yes(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor, On: "2026-09-01", OrgRole: "Contract engineer"})
	if err != nil || !out.Applied {
		t.Fatalf("rejoin: %v", err)
	}
	m := w.get(t, schema.TableActors, w.mira)
	if text(m, schema.FieldActorStatus) != schema.ActorStatusActive || text(m, schema.FieldActorActive) != "true" || text(m, schema.FieldActorLeftAt) != "" {
		t.Fatalf("actor: %v", m.Fields)
	}
	list, err := o.periodList(ctx, PeriodListIn{Actor: "@mira"})
	if err != nil || len(list.Periods) != 2 || list.Periods[0].EndedOn != "2026-06-30" || list.Periods[1].Type != schema.PeriodTypeContractor || list.Periods[1].EndedOn != "" {
		t.Fatalf("periods: %+v %v", list, err)
	}
	for _, id := range []ids.AnyID{w.hubM, w.nwM} {
		if text(w.get(t, schema.TableProjectMembers, id), schema.FieldMemberEndedOn) == "" {
			t.Errorf("membership %s was restored", id)
		}
	}
	if text(w.get(t, schema.TableActors, w.agent), schema.FieldActorActive) != "false" {
		t.Error("the agent was reactivated")
	}
}

// TestPeriodAddEditList: add replaces an open read-time guess, keeps a
// closed one, refuses overlaps; edit fixes a date and re-checks; list
// shows the read-time period without an id.
func TestPeriodAddEditList(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	o := w.ops()
	list, err := o.periodList(ctx, PeriodListIn{Actor: "@mira"})
	if err != nil || len(list.Periods) != 1 || list.Periods[0].ID != "" || list.Periods[0].Source != schema.PeriodSourceMigrated ||
		!strings.Contains(list.Text(), "(from the roster: active/left_at)") {
		t.Fatalf("list: %+v %v", list, err)
	}
	// The read-time guess starts 2026-01-12; the real stint started earlier.
	out, err := o.periodAdd(ctx, PeriodAddIn{Actor: "@mira", Type: schema.PeriodTypeEmployee, StartedOn: "2025-11-03", OrgRole: "Senior engineer"})
	if err != nil || !out.Replaced {
		t.Fatalf("add: %+v %v", out, err)
	}
	if ps := w.f.All(schema.TableActorPeriods); len(ps) != 1 || text(ps[0], schema.FieldPeriodSource) != "" {
		t.Fatalf("rows: %v", ps)
	}
	// A past stint before it.
	if _, err = o.periodAdd(ctx, PeriodAddIn{Actor: "@mira", Type: schema.PeriodTypeIntern, StartedOn: "2024-06-01", EndedOn: "2024-08-31"}); err != nil {
		t.Fatal(err)
	}
	// Overlaps are refused, and so is a second open stint.
	_, err = o.periodAdd(ctx, PeriodAddIn{Actor: "@mira", Type: schema.PeriodTypeIntern, StartedOn: "2024-08-01", EndedOn: "2024-09-30"})
	wantCode(t, err, errs.PeriodOverlap)
	_, err = o.periodAdd(ctx, PeriodAddIn{Actor: "@mira", Type: schema.PeriodTypeContractor, StartedOn: "2026-02-01"})
	wantCode(t, err, errs.PeriodOverlap)
	_, err = o.periodAdd(ctx, PeriodAddIn{Actor: "@mira", Type: schema.PeriodTypeIntern, StartedOn: "2024-08-01", EndedOn: "2024-07-01"})
	wantCode(t, err, errs.PeriodOverlap)
	// Edit: fix the intern end; an edit into an overlap is refused.
	var intern ids.AnyID
	for _, p := range w.f.All(schema.TableActorPeriods) {
		if text(p, schema.FieldPeriodType) == schema.PeriodTypeIntern {
			intern = p.ID
		}
	}
	e, err := o.periodEdit(ctx, PeriodEditIn{Period: intern.String(), EndedOn: "2024-09-15", Note: "extended"})
	if err != nil || e.Period.EndedOn != "2024-09-15" || e.Period.Note != "extended" {
		t.Fatalf("edit: %+v %v", e, err)
	}
	_, err = o.periodEdit(ctx, PeriodEditIn{Period: intern.String(), EndedOn: "2025-12-01"})
	wantCode(t, err, errs.PeriodOverlap)
	_, err = o.periodEdit(ctx, PeriodEditIn{Period: intern.String(), Clear: "type"})
	wantCode(t, err, errs.InvalidInput)
	_, err = o.periodEdit(ctx, PeriodEditIn{Period: "@mira", Note: "x"})
	wantCode(t, err, errs.InvalidInput) // two stints: name one
	if _, err = o.periodEdit(ctx, PeriodEditIn{Period: intern.String(), Clear: "note"}); err != nil {
		t.Fatal(err)
	}
	if text(w.get(t, schema.TableActorPeriods, intern), schema.FieldPeriodNote) != "" {
		t.Error("--clear note")
	}
}

// TestPeriodAddKeepsClosedReadTime: a deactivated (v0.1.17) person's
// read-time period is closed, so it is materialised and kept; an add that
// overlaps it is refused.
func TestPeriodAddKeepsClosedReadTime(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	m := w.get(t, schema.TableActors, w.mira)
	m.Fields[schema.FieldActorActive] = value.BoolOf(false)
	m.Fields[schema.FieldActorLeftAt] = value.DateTimeFrom(time.Date(2026, 5, 31, 18, 0, 0, 0, time.UTC))
	w.f.Put(m)
	o := w.ops()
	_, err := o.periodAdd(ctx, PeriodAddIn{Actor: "@mira", Type: schema.PeriodTypeEmployee, StartedOn: "2026-03-01"})
	wantCode(t, err, errs.PeriodOverlap)
	if _, err = o.periodAdd(ctx, PeriodAddIn{Actor: "@mira", Type: schema.PeriodTypeIntern, StartedOn: "2025-06-01", EndedOn: "2025-08-31"}); err != nil {
		t.Fatal(err)
	}
	if n := len(w.f.All(schema.TableActorPeriods)); n != 2 {
		t.Fatalf("want the read-time period kept beside the new one, got %d rows", n)
	}
	// Editing a person's only stint by actor reference materialises it.
	w = newWorld(t)
	if _, err = w.ops().periodEdit(ctx, PeriodEditIn{Period: "@mira", OrgRole: "Senior engineer"}); err != nil {
		t.Fatal(err)
	}
	if ps := w.f.All(schema.TableActorPeriods); len(ps) != 1 || text(ps[0], schema.FieldPeriodOrgRole) != "Senior engineer" {
		t.Fatalf("rows: %v", ps)
	}
}

// namerEnv is the fake plus the profilestore.ProjectNamer upgrade.
type namerEnv struct {
	*profilestoretest.Fake
	names map[string]string
}

func (e namerEnv) ProjectName(_ context.Context, id string) (string, bool) {
	n, ok := e.names[id]
	return n, ok
}

// TestPreviewProjectNames: a membership whose project row is not on the
// roster store (AllowDangling, TEAM_PROFILES 2.6) shows the name the
// ProjectNamer upgrade gives, in both the offboard and the rejoin preview;
// an Env without the upgrade, or a namer that misses, shows the prj_ id.
func TestPreviewProjectNames(t *testing.T) {
	for name, tc := range map[string]struct {
		namer bool   // the Env has the upgrade
		known bool   // the namer knows the project
		name  string // the label shown; "" means the prj_ id
	}{
		"namer resolves": {namer: true, known: true, name: "far-away"},
		"namer misses":   {namer: true},
		"no namer":       {},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			far := testID(t, ids.PrefixProject, 9) // no projects row on the roster
			w.member(t, far, "2026-02-01", "dev")
			o := ops{env: w.f}
			if tc.namer {
				names := map[string]string{}
				if tc.known {
					names[far.String()] = tc.name
				}
				o = ops{env: namerEnv{Fake: w.f, names: names}}
			}
			label := tc.name
			if label == "" {
				label = far.String()
			}
			_, err := o.offboard(context.Background(), OffboardIn{Actor: "@mira", On: "2026-06-30"})
			wantCode(t, err, errs.NeedsConfirm)
			if q := err.(*errs.Error).Question; !strings.Contains(q, label+" (dev) ends 2026-06-30") {
				t.Errorf("offboard preview misses %q:\n%s", label, q)
			}
			if _, err = o.offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-06-30"}); err != nil {
				t.Fatal(err)
			}
			_, err = o.rejoin(context.Background(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor, On: "2026-09-01"})
			wantCode(t, err, errs.NeedsConfirm)
			if q := err.(*errs.Error).Question; !strings.Contains(q, label+" (dev): lore project member add "+label+" ") {
				t.Errorf("rejoin preview misses %q:\n%s", label, q)
			}
		})
	}
}

// TestWritesCarryEngineWarnings: every write op of this package copies
// the roster's engine warnings into its output (JSON warnings and one
// line after the success line); a run that writes nothing has none.
func TestWritesCarryEngineWarnings(t *testing.T) {
	const warn = "W_URL_CREDENTIALS note: removed a credential"
	type wrote interface{ Text() string }
	cases := []struct {
		name string
		run  func(o ops) (wrote, []string, error)
	}{
		{"status on_leave", func(o ops) (wrote, []string, error) {
			out, err := o.status(context.Background(), ActorStatusIn{Actor: "@mira", To: schema.ActorStatusOnLeave})
			return out, out.Warnings, err
		}},
		{"offboard", func(o ops) (wrote, []string, error) {
			out, err := o.offboard(yes(), OffboardIn{Actor: "@mira"})
			return out, out.Warnings, err
		}},
		{"period add", func(o ops) (wrote, []string, error) {
			out, err := o.periodAdd(context.Background(), PeriodAddIn{Actor: "@mira", Type: schema.PeriodTypeIntern, StartedOn: "2024-06-01", EndedOn: "2024-08-31"})
			return out, out.Warnings, err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.f.WarnOnWrite = warn
			out, ws, err := c.run(w.ops())
			if err != nil {
				t.Fatal(err)
			}
			if len(ws) == 0 || ws[0] != warn || !strings.Contains(out.Text(), "\n"+warn+"\n") {
				t.Fatalf("warnings %q, text:\n%s", ws, out.Text())
			}
		})
	}
	// rejoin after an offboard: the rejoin's own writes only
	w := newWorld(t)
	if _, err := w.ops().offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-06-30"}); err != nil {
		t.Fatal(err)
	}
	w.f.WarnOnWrite = warn
	out, err := w.ops().rejoin(yes(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor})
	if err != nil || len(out.Warnings) != 2 || !strings.HasSuffix(out.Text(), warn+"\n") {
		t.Fatalf("rejoin %q %v", out.Warnings, err)
	}
	// a no-op writes nothing and warns nothing
	w = newWorld(t)
	w.f.WarnOnWrite = warn
	noop, err := w.ops().status(context.Background(), ActorStatusIn{Actor: "@mira", To: schema.ActorStatusActive})
	if err != nil || len(noop.Warnings) != 0 {
		t.Fatalf("no-op %+v %v", noop, err)
	}
}

// TestOffboardNotStarted (break-verify target): a membership that starts
// after today (member add --on <future>) cannot be ended before it began,
// so the offboard previews it as "not started yet (starts <date>): ends
// unstarted" and the confirmed run moves it to the trash with
// MemberTrashReasonUnstarted (restorable), while the started ones end on
// --on; no membership outlives the stint. A membership that started
// after a past --on (but by today) is still E_PERIOD_OVERLAP.
func TestOffboardNotStarted(t *testing.T) {
	w := newWorld(t)
	lab := testID(t, ids.PrefixProject, 6)
	w.f.Put(row.Row{Table: schema.TableProjects, ID: lab, Fields: map[string]value.Value{"name": txt(t, "lab")}})
	future := w.member(t, lab, "2026-10-15", "qa")
	o := w.ops()
	before := w.snapshot()
	_, err := o.offboard(context.Background(), OffboardIn{Actor: "@mira"})
	wantCode(t, err, errs.NeedsConfirm)
	if !strings.Contains(err.Error(), "lab (qa) not started yet (starts 2026-10-15): ends unstarted") ||
		!strings.Contains(err.Error(), "acme-hub (dev, reviewer) ends 2026-09-30") {
		t.Errorf("preview: %v", err)
	}
	if !reflect.DeepEqual(before, w.snapshot()) {
		t.Fatal("the preview wrote")
	}
	out, err := o.offboard(yes(), OffboardIn{Actor: "@mira"})
	if err != nil || !out.Applied {
		t.Fatalf("offboard: %+v %v", out, err)
	}
	var got MemberEnd
	for _, m := range out.Memberships {
		if m.ID == future.String() {
			got = m
		}
	}
	if !strings.Contains(out.Text(), "(2 memberships ended, 1 not started moved to the trash, 1 agents inactive, 1 open tasks listed above)") {
		t.Errorf("text: %s", out.Text())
	}
	if !got.NotStarted || got.StartsOn != "2026-10-15" || got.EndsOn != "" {
		t.Errorf("json: %+v", got)
	}
	m := w.get(t, schema.TableProjectMembers, future)
	if text(m, schema.FieldMemberEndedOn) != "" || text(m, trash.FieldDeletedReason) != MemberTrashReasonUnstarted {
		t.Errorf("not-started membership after offboard: %v", m.Fields)
	}
	rr, done, err := w.f.Roster(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if err := rr.InTrash(context.Background(), schema.TableProjectMembers, future); !errs.Has(err, errs.InTrash) {
		t.Errorf("not in the trash: %v", err)
	}
	if h := w.get(t, schema.TableProjectMembers, w.hubM); text(h, schema.FieldMemberEndedOn) != "2026-09-30" {
		t.Errorf("started membership: %v", h.Fields)
	}

	// A past --on before a membership that has started by today: refused.
	w2 := newWorld(t)
	_, err = w2.ops().offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-02-01"})
	wantCode(t, err, errs.PeriodOverlap)
}

// trashEnv is the Fake with a roster that lists its trash (TrashLister),
// as wire's does.
type trashEnv struct{ *profilestoretest.Fake }

func (e trashEnv) Roster(ctx context.Context, write bool) (profilestore.Roster, func(), error) {
	r, done, err := e.Fake.Roster(ctx, write)
	return trashRoster{Roster: r, f: e.Fake}, done, err
}

type trashRoster struct {
	profilestore.Roster
	f *profilestoretest.Fake
}

// ListTrash implements TrashLister: the fake's rows carrying a delete
// reason (how the fake marks a row in the trash), tombstones left out.
func (r trashRoster) ListTrash(_ context.Context, table row.Table) ([]row.Row, error) {
	var out []row.Row
	for _, x := range r.f.All(table) {
		if _, in := x.Fields[trash.FieldDeletedReason]; in && !x.Deleted {
			out = append(out, x)
		}
	}
	return out, nil
}

// TestRejoinListsTrashedUnstarted: a membership the offboard moved to the
// trash before it began is listed by the rejoin, in the preview and the
// done text, with `lore trash restore <id>`, and in --json
// (trashed_memberships, not_started, starts_on); the rejoin does not
// restore it. A roster that cannot
// list its trash lists none ([] in JSON).
func TestRejoinListsTrashedUnstarted(t *testing.T) {
	w := newWorld(t)
	lab := testID(t, ids.PrefixProject, 6)
	w.f.Put(row.Row{Table: schema.TableProjects, ID: lab, Fields: map[string]value.Value{"name": txt(t, "lab")}})
	future := w.member(t, lab, "2026-10-15", "qa")
	if _, err := w.ops().offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-09-01"}); err != nil {
		t.Fatal(err)
	}
	line := "lab (qa) starting 2026-10-15, " + MemberTrashReasonUnstarted + ": lore trash restore " + future.String()
	o := ops{env: trashEnv{Fake: w.f}}
	_, err := o.rejoin(context.Background(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor, On: "2026-09-30"})
	wantCode(t, err, errs.NeedsConfirm)
	if q := err.(*errs.Error).Question; !strings.Contains(q, "  trashed    "+line+"\n") {
		t.Errorf("preview misses the trashed membership:\n%s", q)
	}
	out, err := o.rejoin(yes(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor, On: "2026-09-30"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text(), line) || !strings.Contains(out.Text(), "rejoined on 2026-09-30") {
		t.Errorf("done text:\n%s", out.Text())
	}
	if len(out.TrashedMemberships) != 1 {
		t.Fatalf("trashed_memberships: %+v", out.TrashedMemberships)
	}
	if m := out.TrashedMemberships[0]; m.ID != future.String() || m.Project != "lab" || !m.NotStarted || m.StartsOn != "2026-10-15" || m.EndsOn != "" {
		t.Errorf("json: %+v", m)
	}
	if m := w.get(t, schema.TableProjectMembers, future); text(m, trash.FieldDeletedReason) != MemberTrashReasonUnstarted {
		t.Errorf("the rejoin touched the trashed membership: %v", m.Fields)
	}
	for _, m := range out.Memberships {
		if m.ID == future.String() {
			t.Errorf("trashed membership listed as ended: %+v", m)
		}
	}

	// Without the upgrade: none, and the JSON field is [] (never null).
	w2 := newWorld(t)
	w2.member(t, lab, "2026-10-15", "qa")
	if _, err = w2.ops().offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-09-01"}); err != nil {
		t.Fatal(err)
	}
	plain, err := ops{env: w2.f}.rejoin(yes(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor, On: "2026-09-30"})
	if err != nil || plain.TrashedMemberships == nil || len(plain.TrashedMemberships) != 0 || strings.Contains(plain.Text(), "trashed") {
		t.Errorf("no TrashLister: %+v %v", plain.TrashedMemberships, err)
	}
}

// TestOffboardTakesThemOffTeams (B2-12): offboard (and member remove,
// which runs it) takes the person off every team that lists them, says so
// in the preview, and leaves the other members and other teams as they
// were; before, member show still listed their teams.
func TestOffboardTakesThemOffTeams(t *testing.T) {
	w := newWorld(t)
	web, ops := testID(t, ids.PrefixTeam, 1), testID(t, ids.PrefixTeam, 2)
	list := func(members ...ids.AnyID) value.Value {
		vs := make([]value.Value, 0, len(members))
		for _, m := range members {
			vs = append(vs, ref(t, m))
		}
		l, err := value.ListOf(value.KindRef, vs...)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	w.f.Put(row.Row{Table: schema.TableTeams, ID: web, Fields: map[string]value.Value{"handle": txt(t, "web"), "project_id": ref(t, w.hub), schema.FieldTeamMembers: list(w.mira, w.sam)}})
	w.f.Put(row.Row{Table: schema.TableTeams, ID: ops, Fields: map[string]value.Value{"handle": txt(t, "ops"), "project_id": ref(t, w.hub), schema.FieldTeamMembers: list(w.sam)}})
	_, err := w.ops().offboard(context.Background(), OffboardIn{Actor: "@mira", On: "2026-09-30"})
	if err == nil || !strings.Contains(err.(*errs.Error).Question, "teams      @web: taken off") {
		t.Fatalf("preview does not name the team: %v", err)
	}
	out, err := w.ops().offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-09-30"})
	if err != nil || !slices.Equal(out.Teams, []string{"web"}) {
		t.Fatalf("offboard: %+v %v", out, err)
	}
	if got := value.ItemStrings(w.get(t, schema.TableTeams, web).Fields[schema.FieldTeamMembers]); !slices.Equal(got, []string{w.sam.String()}) {
		t.Errorf("@web members after offboard: %v", got)
	}
	if got := value.ItemStrings(w.get(t, schema.TableTeams, ops).Fields[schema.FieldTeamMembers]); !slices.Equal(got, []string{w.sam.String()}) {
		t.Errorf("@ops changed: %v", got)
	}
}
