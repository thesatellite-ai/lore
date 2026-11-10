package wire

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsperiod "github.com/thesatellite-ai/lore/ops/period"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Fixture actors of the org roster.
const (
	periodsHere = "act_01a1a59b891c70b789144cd3e711fa01" // active person
	periodsGone = "act_01a1a59b891c70b789144cd3e711fa02" // deactivated person (v0.1.17)
	periodsBot  = "act_01a1a59b891c70b789144cd3e711fa03" // agent: no period
)

// orgPeriodsApp is an App in a git repo of org agency whose roster store
// is rosterStore, with three roster actors.
func orgPeriodsApp(t *testing.T) *App {
	t.Helper()
	return orgPeriodsAppIn(t, gitRepo(t))
}

// orgPeriodsAppIn is orgPeriodsApp in the git repo at repo.
func orgPeriodsAppIn(t *testing.T, repo string) *App {
	t.Helper()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	a := openWith(t, repo, Options{
		OrgStores: func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
		},
		Org: func(context.Context) (*identity.Org, error) {
			return &identity.Org{Name: "agency", Identity: identity.Entry{Key: testEmail, Handle: "alice"}}, nil
		},
		RepoIsClient: func(context.Context) (bool, error) { return false, nil },
	})
	withProject(t, a)
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	for id, f := range map[string]map[string]string{
		periodsHere: {"kind": schema.ActorKindHuman, "display_name": "Mira"},
		periodsGone: {"kind": schema.ActorKindHuman, "display_name": "Carol"},
		periodsBot:  {"kind": schema.ActorKindAgent, "display_name": "bot"},
	} {
		aid, perr := ids.ParseAny(id)
		if perr != nil {
			t.Fatal(perr)
		}
		fields := textValues(t, f)
		if id == periodsGone {
			fields[schema.FieldActorActive] = value.BoolOf(false)
			fields[schema.FieldActorLeftAt] = value.DateTimeFrom(time.Now().UTC())
		}
		if _, err = eng.CreateWithID(background(), rosterStore, schema.TableActors, aid, fields); err != nil {
			t.Fatal(err)
		}
	}
	asV017Rows(t, a, periodsHere, periodsGone, periodsBot)
	return a
}

// asV017Rows strips the `_o` block 0.1.18 stamps on create from the roster
// actor rows ids, so they read as rows v0.1.17 wrote (it had no
// provenance), the rows the actor_periods step materialises (#22d).
func asV017Rows(t *testing.T, a *App, actorIDs ...string) {
	t.Helper()
	ctx := background()
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := st.byName[rosterStore]
	if !ok {
		t.Fatalf("roster store %s not attached", rosterStore)
	}
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var changes []store.Change
	for _, sr := range rows {
		if sr.Table != schema.TableActors || !slices.Contains(actorIDs, sr.ID.String()) {
			continue
		}
		r, derr := sr.Decode()
		if derr != nil {
			t.Fatal(derr)
		}
		r.Origin = value.JSON{}
		up, ferr := store.FromRow(r)
		if ferr != nil {
			t.Fatal(ferr)
		}
		changes = append(changes, store.Change{Row: up, Expect: sr.HLC})
	}
	if len(changes) != len(actorIDs) {
		t.Fatalf("found %d of %d actor rows", len(changes), len(actorIDs))
	}
	if err = b.Write(ctx, changes); err != nil {
		t.Fatal(err)
	}
	if _, err = st.eng.Refresh(ctx, rosterStore); err != nil {
		t.Fatal(err)
	}
}

// livePeriods lists the live actor_periods rows of the roster store.
func livePeriods(t *testing.T, a *App) []row.Row {
	t.Helper()
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := eng.List(background(), rosterStore, schema.TableActorPeriods)
	if err != nil {
		t.Fatal(err)
	}
	var out []row.Row
	for _, r := range rows {
		if !r.Deleted {
			out = append(out, r)
		}
	}
	return out
}

// TestMigrateProfilesActorPeriods (TP-22): a dry run writes nothing; the
// run writes one migrated period per person (open for the active one,
// ended on the left_at day for the deactivated one, none for the agent),
// keeps active/left_at, and a second run writes nothing (F-MIGRATE-1).
func TestMigrateProfilesActorPeriods(t *testing.T) {
	t.Parallel()
	a := orgPeriodsApp(t)
	dry, n, err := a.migrateActorPeriods(background(), true)
	if err != nil || n != 2 || len(dry) != 1 || len(livePeriods(t, a)) != 0 {
		t.Fatalf("dry run: %v %d %v (rows %d)", dry, n, err, len(livePeriods(t, a)))
	}
	steps, n, err := a.migrateActorPeriods(background(), false)
	if err != nil || n != 2 || len(steps) != 1 {
		t.Fatalf("run: %v %d %v", steps, n, err)
	}
	today := time.Now().UTC().Format(value.DateLayout)
	got := map[string]row.Row{}
	for _, p := range livePeriods(t, a) {
		got[fieldText(p, schema.FieldPeriodActor)] = p
	}
	here, gone := got[periodsHere], got[periodsGone]
	if len(got) != 2 || fieldText(here, schema.FieldPeriodEndedOn) != "" || fieldText(here, schema.FieldPeriodStartedOn) != today ||
		fieldText(gone, schema.FieldPeriodEndedOn) != today || fieldText(gone, schema.FieldPeriodSource) != schema.PeriodSourceMigrated ||
		fieldText(here, schema.FieldPeriodType) != schema.PeriodTypeEmployee {
		t.Fatalf("periods: %v", got)
	}
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	actors, err := eng.List(background(), rosterStore, schema.TableActors)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range actors {
		if x.ID.String() == periodsGone && (fieldText(x, schema.FieldActorActive) != "false" || fieldText(x, schema.FieldActorLeftAt) == "") {
			t.Fatalf("active/left_at lost: %v", x.Fields)
		}
	}
	again, n, err := a.migrateActorPeriods(background(), false)
	if err != nil || n != 0 || len(again) != 0 || len(livePeriods(t, a)) != 2 {
		t.Fatalf("second run: %v %d %v", again, n, err)
	}
}

// TestMigrateProfilesNoOrg (TS-1): a repo without an org writes no period.
func TestMigrateProfilesNoOrg(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, gitRepo(t))
	withProject(t, a)
	steps, n, err := a.migrateActorPeriods(background(), false)
	if err != nil || n != 0 || len(steps) != 0 {
		t.Fatalf("%v %d %v", steps, n, err)
	}
}

// TestTaskReaderOpenTasks (TP-17): lead and assignee of open tasks are
// listed, done/cancelled tasks and other people's are not.
func TestTaskReaderOpenTasks(t *testing.T) {
	t.Parallel()
	a := orgPeriodsApp(t)
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	prj, err := ids.ParseAny(projectID(t, a))
	if err != nil {
		t.Fatal(err)
	}
	mira, err := ids.ParseAny(periodsHere)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ids.ParseAny(periodsBot)
	if err != nil {
		t.Fatal(err)
	}
	refOf := func(id ids.AnyID) value.Value {
		v, rerr := value.RefOf(id)
		if rerr != nil {
			t.Fatal(rerr)
		}
		return v
	}
	listOf := func(id ids.AnyID) value.Value {
		v, lerr := value.ListOf(value.KindText, textValues(t, map[string]string{"x": id.String()})["x"])
		if lerr != nil {
			t.Fatal(lerr)
		}
		return v
	}
	add := func(title, status string, lead ids.AnyID, assignees value.Value) string {
		f := textValues(t, map[string]string{"title": title, "status": status,
			"priority": schema.TaskPriorities()[0], "commitment": schema.TaskCommitments()[0]})
		f["project_id"] = refOf(prj)
		f[fieldTaskLead] = refOf(lead)
		if assignees != nil {
			f[schema.FieldTaskAssignees] = assignees
		}
		w, cerr := eng.Create(background(), store.SourceRepo, schema.TableTasks, f)
		if cerr != nil {
			t.Fatal(cerr)
		}
		return w.Row.ID.String()
	}
	lead := add("lead it", schema.TaskStatusInProgress, mira, nil)
	assignee := add("help", schema.TaskStatusBlocked, other, listOf(mira))
	add("finished", schema.TaskStatusDone, mira, nil)
	add("someone else's", schema.TaskStatusTodo, other, listOf(other))
	got, err := a.OpenTasks(background(), mira)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{lead: opsperiod.TaskOnLead, assignee: opsperiod.TaskOnAssignee}
	if len(got) != len(want) {
		t.Fatalf("open tasks: %+v", got)
	}
	for _, x := range got {
		if want[x.ID] != x.How || x.Store != string(store.SourceRepo) {
			t.Fatalf("open tasks: %+v", got)
		}
	}
}

// TestMigrateProfilesSkips018Rows (#22d): a roster row 0.1.18 wrote (an
// `org create` or `org import` register, stamped with `_o`) gets no
// migrated period, so a fresh 0.1.18 org reports no actor_periods step,
// while the v0.1.17 rows beside it still do; the 0.1.18 person's period is
// still read from active/left_at.
func TestMigrateProfilesSkips018Rows(t *testing.T) {
	t.Parallel()
	a := orgPeriodsApp(t)
	const fresh = "act_01a1a59b891c70b789144cd3e711fa04"
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	aid, err := ids.ParseAny(fresh)
	if err != nil {
		t.Fatal(err)
	}
	w, err := eng.CreateWithID(background(), rosterStore, schema.TableActors, aid, textValues(t, map[string]string{"kind": schema.ActorKindHuman, "display_name": "Ada"}))
	if err != nil || w.Row.Origin.IsZero() {
		t.Fatalf("a 0.1.18 create carries _o: %v %v", w.Row.Origin, err)
	}
	steps, n, err := a.migrateActorPeriods(background(), false)
	if err != nil || n != 2 || len(steps) != 1 || !strings.Contains(steps[0], "wrote 2 company period(s)") {
		t.Fatalf("run: %v %d %v", steps, n, err)
	}
	for _, p := range livePeriods(t, a) {
		if fieldText(p, schema.FieldPeriodActor) == fresh {
			t.Fatalf("the 0.1.18 row got a migrated period: %v", p.Fields)
		}
	}
	actors, err := eng.List(background(), rosterStore, schema.TableActors)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range actors {
		if x.ID.String() != fresh {
			continue
		}
		ps, perr := teamprofile.PeriodsOf(x, livePeriods(t, a))
		if perr != nil || len(ps) != 1 || !ps[0].Synthesised() || ps[0].Type != schema.PeriodTypeEmployee {
			t.Fatalf("the 0.1.18 person's period is still read: %+v %v", ps, perr)
		}
	}

}
