package wire

// coord_standalone_views_test.go — lore inbox and lore mission status on
// cmd/lore's coordination host (coord_standalone_views.go) over real row
// files: later.md 212 (authors from the row envelope).

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// bobEmail is the second person of the coordination view tests.
const bobEmail = "bob@example.com"

// viewFixture is a repo with a project, the App of testEmail (me) and the
// App of bobEmail over the same repo, and both actor ids.
type viewFixture struct {
	me, bob     *App
	meID, bobID string
	project     string
}

func newViewFixture(t *testing.T) viewFixture {
	t.Helper()
	repo := gitRepo(t)
	notClient := func(context.Context) (bool, error) { return false, nil }
	me := openWith(t, repo, Options{RepoIsClient: notClient})
	withProject(t, me)
	bob, err := New(Options{RepoDir: repo, FlagActor: bobEmail, Home: t.TempDir(), RepoIsClient: notClient})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bob.Close() })
	f := viewFixture{me: me, bob: bob}
	for _, x := range []struct {
		a   *App
		out *string
	}{{me, &f.meID}, {bob, &f.bobID}} {
		id, aerr := x.a.Actor(background())
		if aerr != nil {
			t.Fatal(aerr)
		}
		*x.out = id.String()
	}
	// The actor rows the views name people by (an org roster in real use).
	for _, x := range []struct{ id, handle, name string }{{f.meID, "me", "Me Myself"}, {f.bobID, "bob", "Bob Builder"}} {
		aid, perr := ids.ParseAny(x.id)
		if perr != nil {
			t.Fatal(perr)
		}
		eng, eerr := me.Engine(background())
		if eerr != nil {
			t.Fatal(eerr)
		}
		if _, _, gerr := me.Get(background(), engine.Ref{Table: schema.TableActors, ID: aid}); gerr == nil {
			continue
		}
		if _, cerr := eng.CreateWithID(background(), store.SourceRepo, schema.TableActors, aid,
			textValues(t, map[string]string{"kind": schema.ActorKindHuman, "display_name": x.name, "handle": x.handle})); cerr != nil {
			t.Fatal(cerr)
		}
	}
	p, err := me.Project(background())
	if err != nil {
		t.Fatal(err)
	}
	f.project = p.String()
	return f
}

// create writes one row as a's actor through the engine, with text fields
// only (no created_by_actor_id: the author is the envelope's created_by).
func (f viewFixture) create(t *testing.T, a *App, table row.Table, fields map[string]string) string {
	t.Helper()
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]value.Value{"project_id": mustText(t, f.project)}
	for k, v := range fields {
		vals[k] = mustText(t, v)
	}
	w, err := eng.Create(background(), store.SourceRepo, table, vals)
	if err != nil {
		t.Fatalf("create %s: %v", table, err)
	}
	if _, has := w.Row.Fields["created_by_actor_id"]; has {
		t.Fatalf("the fixture row must not carry created_by_actor_id: %v", w.Row.Fields)
	}
	return w.Row.ID.String()
}

// inboxItems runs lore inbox as a.
func inboxItems(t *testing.T, a *App) []opscoord.InboxItem {
	t.Helper()
	out, err := run(t, a, opscoord.OpInbox, `{}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var got opscoord.InboxOut
	if err = json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	return got.Data
}

// TestInboxAuthorsFromTheEnvelope (later.md 212): rows written by the core
// carry no created_by_actor_id; the views read the author from the row
// envelope's created_by. The mission I created is mine (its findings reach
// me), a finding bob wrote names bob, a handoff to me from bob shows, and
// bob, who owns nothing, sees none of them.
func TestInboxAuthorsFromTheEnvelope(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t)
	mission := f.create(t, f.me, schema.TableMissions, map[string]string{"title": "ship billing", "status": schema.MissionStatusActive})
	f.create(t, f.bob, schema.TableMemories, map[string]string{"kind": schema.MemoryKindCore, "body": "the cache key misses the tenant",
		"source_kind": findingKind, "source_ref": mission})
	f.create(t, f.bob, schema.TableHandoffs, map[string]string{"body": "take over the invoices", "status_str": handoffOpen,
		"from_actor_id": f.bobID, "to_actor_id": f.meID, "mission_id": mission})
	f.create(t, f.bob, schema.TableHandoffs, map[string]string{"body": "already taken", "status_str": "accepted",
		"from_actor_id": f.bobID, "to_actor_id": f.meID, "mission_id": mission})
	kinds := map[string]opscoord.InboxItem{}
	for _, it := range inboxItems(t, f.me) {
		if _, dup := kinds[it.Kind]; dup {
			t.Fatalf("one %s item expected: %+v", it.Kind, it)
		}
		kinds[it.Kind] = it
	}
	fd, ok := kinds[inboxFinding]
	if !ok || fd.Mission != mission || fd.Text != "the cache key misses the tenant" || fd.Who != "@bob" {
		t.Fatalf("the finding on my mission, by bob: %+v (all %v)", fd, kinds)
	}
	hd, ok := kinds[inboxHandoff]
	if !ok || hd.Mission != mission || hd.Who != fd.Who || hd.Text != "take over the invoices" {
		t.Fatalf("the open handoff to me from bob: %+v", hd)
	}
	if len(kinds) != 2 {
		t.Fatalf("only the finding and the open handoff: %v", kinds)
	}
	out, err := run(t, f.me, opscoord.OpMissionStatus, `{"id":"`+mission+`"}`, true)
	var st opscoord.MissionStatusOut
	if err != nil || json.Unmarshal([]byte(out), &st) != nil || st.Data.Mission != mission || st.Data.Title != "ship billing" {
		t.Fatalf("mission status of my mission: %s %v", out, err)
	}
	if got := inboxItems(t, f.bob); len(got) != 0 {
		t.Fatalf("bob owns no mission and has no handoff: %+v", got)
	}
}

// TestInboxListsForcedReveals (later.md 272, SECRETS 16 / SQ10): with the
// chain sink, every forced reveal in the audit chain is a FORCED inbox
// item naming who revealed what; an ordinary reveal and other events are
// not; --ack hides it until the next forced reveal; without a chain
// (no sink) the inbox lists none and does not fail.
func TestInboxListsForcedReveals(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	home := t.TempDir()
	notClient := func(context.Context) (bool, error) { return false, nil }
	a := openWith(t, repo, Options{Home: home, RepoIsClient: notClient, Audit: NewChainAudit(home, repo, time.Now)})
	withProject(t, a)
	actor, err := a.Actor(background())
	if err != nil {
		t.Fatal(err)
	}
	reveal := func(forced engine.ForcedBy, rowID string) {
		t.Helper()
		id, perr := ids.ParseAny(rowID)
		if perr != nil {
			t.Fatal(perr)
		}
		ev := engine.Event{Op: "secrets.show", Action: engine.ActionSecretRevealed, Store: store.SourceRepo, Table: schema.TableMemories, ID: id, Actor: actor,
			Secret: engine.SecretAudit{Command: "secrets show", Result: "present", Forced: forced != engine.ForcedNone, ForcedBy: forced}}
		if rerr := a.opts.Audit.Record(background(), ev); rerr != nil {
			t.Fatal(rerr)
		}
	}
	forcedOf := func(items []opscoord.InboxItem) []opscoord.InboxItem {
		var out []opscoord.InboxItem
		for _, it := range items {
			if it.Kind == inboxForced {
				out = append(out, it)
			}
		}
		return out
	}
	reveal(engine.ForcedNone, fixtureMem)
	if got := forcedOf(inboxItems(t, a)); len(got) != 0 {
		t.Fatalf("an ordinary reveal is not FORCED: %+v", got)
	}
	reveal(engine.ForcedFlag, fixtureMem)
	got := forcedOf(inboxItems(t, a))
	if len(got) != 1 || !strings.Contains(got[0].Text, "secret revealed without a person's answer") || !strings.Contains(got[0].Text, "FORCED (flag)") ||
		!strings.Contains(got[0].Text, "(row "+fixtureMem+")") || got[0].Who == "" || got[0].At.IsZero() {
		t.Fatalf("one FORCED item for the forced reveal: %+v", got)
	}
	if _, err = run(t, a, opscoord.OpInbox, `{"ack":true}`, true); err != nil {
		t.Fatal(err)
	}
	if got = forcedOf(inboxItems(t, a)); len(got) != 0 {
		t.Fatalf("an acked forced reveal is not shown again: %+v", got)
	}
	time.Sleep(1100 * time.Millisecond) // the chain stamps whole seconds: the next reveal is after the ack
	reveal(engine.ForcedPolicy, fixtureMem)
	if got = forcedOf(inboxItems(t, a)); len(got) != 1 || !strings.Contains(got[0].Text, "FORCED (policy)") {
		t.Fatalf("a new forced reveal after the ack shows: %+v", got)
	}
	// No chain sink: nothing to list, no failure.
	plain := openWith(t, gitRepo(t), Options{RepoIsClient: notClient})
	withProject(t, plain)
	if got = forcedOf(inboxItems(t, plain)); len(got) != 0 {
		t.Fatalf("no chain, no FORCED items: %+v", got)
	}
}

// TestAssignedBy (later.md 630, port of coord_inbox_assigned_test.go): the
// inbox names who made me lead or added me as an assignee (TASK 20),
// never my own change, and not a list I was already in.
func TestAssignedBy(t *testing.T) {
	t.Parallel()
	const me, other = "act_me", "act_other"
	w := taskWatch{ID: "tsk_1", Lead: me, Assignees: []string{me, other}, History: []opstask.HistoryEntry{
		{ID: "h1", At: "2026-11-05T00:00:00Z", By: other, Field: schema.FieldTaskAssignees, From: "", To: me},
		{ID: "h2", At: "2026-11-05T00:01:00Z", By: me, Field: fieldTaskLead, From: "", To: me},
		{ID: "h3", At: "2026-11-05T00:02:00Z", By: other, Field: schema.FieldTaskAssignees, From: me, To: me + "," + other},
	}}
	if got := w.assignedRole(me); got != roleLead {
		t.Fatalf("role = %q, want lead", got)
	}
	if _, ok := w.assignedBy(me, roleLead); ok {
		t.Fatal("my own lead change must not be news")
	}
	e, ok := w.assignedBy(me, roleAssignee)
	if !ok || e.ID != "h1" {
		t.Fatalf("assignee entry = %+v %v, want h1 (h3 kept me, it did not add me)", e, ok)
	}
	if got := (taskWatch{Assignees: []string{other}}).assignedRole(me); got != "" {
		t.Fatalf("role = %q, want none", got)
	}
	if got := (taskWatch{Assignees: []string{me}}).assignedRole(me); got != roleAssignee {
		t.Fatalf("role = %q, want assignee", got)
	}
}

// TestMetaReviewText (later.md 630, port of inbox_metakeys_test.go): the
// review-queue line (METADATA 3.14 sample): count, distinct authors in
// first-seen order, names once each, the hint; the singular; and a key row
// without a writer (no "added by").
func TestMetaReviewText(t *testing.T) {
	t.Parallel()
	who := map[string]string{"act_a": "tomas via codex", "act_b": "mira via claude_code"}
	author := func(id string) string { return who[id] }
	cases := []struct {
		name string
		keys []opssettings.UnreviewedKey
		want string
	}{
		{"design sample", []opssettings.UnreviewedKey{{Name: "customer_tier", CreatedBy: "act_a"}, {Name: "renewal_risk", CreatedBy: "act_a"}, {Name: "root_cause", CreatedBy: "act_a"}},
			"3 keys await review (added by tomas via codex): customer_tier, renewal_risk, root_cause — lore meta keys --unreviewed"},
		{"two authors, a name in two stores", []opssettings.UnreviewedKey{{Name: "a", CreatedBy: "act_b", Store: "repo"}, {Name: "a", CreatedBy: "act_a", Store: "kb"}, {Name: "b", CreatedBy: "act_a"}},
			"2 keys await review (added by mira via claude_code, tomas via codex): a, b — lore meta keys --unreviewed"},
		{"one key", []opssettings.UnreviewedKey{{Name: "solo", CreatedBy: "act_a"}},
			"1 key awaits review (added by tomas via codex): solo — lore meta keys --unreviewed"},
		{"no writer", []opssettings.UnreviewedKey{{Name: "old"}},
			"1 key awaits review: old — lore meta keys --unreviewed"},
		{"a writer the roster cannot name", []opssettings.UnreviewedKey{{Name: "x", CreatedBy: "act_gone"}},
			"1 key awaits review: x — lore meta keys --unreviewed"},
	}
	for _, c := range cases {
		if got := metaReviewText(c.keys, author); got != c.want {
			t.Errorf("%s:\ngot  %q\nwant %q", c.name, got, c.want)
		}
	}
}

// TestParentCycleItems (later.md 630, port of inbox_parentcycle_test.go):
// one item per cycle holding an open task of mine, named from the smallest
// id; a cycle of only others' tasks, or only closed ones, gives none; a
// plain tree gives none.
func TestParentCycleItems(t *testing.T) {
	t.Parallel()
	const me, other = "act_me", "act_other"
	doc := func(id, parent, status, lead string) coord.Doc {
		d := coord.Doc{}
		for k, v := range map[string]string{docKeyID: id, vkParent: parent, "status": status, fieldTaskLead: lead, vkMission: "msn_1"} {
			d.SetStr(k, v)
		}
		return d
	}
	mine := func(d coord.Doc) bool { return d.Str(fieldTaskLead) == me }
	cases := []struct {
		name string
		docs []coord.Doc
		want []string
	}{
		{"two-task cycle with my open task", []coord.Doc{doc("tsk_b", "tsk_a", "todo", me), doc("tsk_a", "tsk_b", "todo", other)},
			[]string{"parent cycle tsk_a → tsk_b → tsk_a: tsk_a reads as top level"}},
		{"three-task cycle, mine in the middle", []coord.Doc{doc("tsk_a", "tsk_c", "todo", other), doc("tsk_b", "tsk_a", "in_progress", me), doc("tsk_c", "tsk_b", "done", other)},
			[]string{"tsk_a → tsk_b → tsk_c → tsk_a"}},
		{"cycle of others only", []coord.Doc{doc("tsk_a", "tsk_b", "todo", other), doc("tsk_b", "tsk_a", "todo", other)}, nil},
		{"my tasks in the cycle are closed", []coord.Doc{doc("tsk_a", "tsk_b", "done", me), doc("tsk_b", "tsk_a", "cancelled", me)}, nil},
		{"plain tree", []coord.Doc{doc("tsk_a", "", "todo", me), doc("tsk_b", "tsk_a", "todo", me)}, nil},
		{"self parent is not a cycle", []coord.Doc{doc("tsk_a", "tsk_a", "todo", me)}, nil},
		{"two cycles, one mine", []coord.Doc{doc("tsk_a", "tsk_b", "todo", me), doc("tsk_b", "tsk_a", "todo", other),
			doc("tsk_x", "tsk_y", "todo", other), doc("tsk_y", "tsk_x", "todo", other)}, []string{"tsk_a → tsk_b"}},
	}
	for _, tc := range cases {
		got := cycleItems(tc.docs, mine)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %d items %+v, want %d", tc.name, len(got), got, len(tc.want))
		}
		for i, w := range tc.want {
			if got[i].Kind != inboxParent || got[i].Mission != "msn_1" || !strings.Contains(got[i].Text, w) {
				t.Fatalf("%s: item %d = %+v, want kind %q mission msn_1 text containing %q", tc.name, i, got[i], inboxParent, w)
			}
		}
	}
}

// TestMissionSLAColumn (later.md 630, port of mission_sla_test.go):
// mission status's SLA column (18.4) is breached past the priority's
// hours, at_risk from 80%, ok before, and empty (not counted) for a
// priority with no SLA or with no policy at all; a late open row's detail
// says so, a done one's does not.
func TestMissionSLAColumn(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	hours := map[string]int64{"urgent": 10}
	task := func(prio, status string, age time.Duration) coord.Doc {
		d := coord.Doc{}
		d.SetStr(docKeyID, "tsk_x")
		d.SetStr(vkPriority, prio)
		d.SetStr("status", status)
		d.SetTime(docKeyCreatedAt, now.Add(-age))
		return d
	}
	cases := []struct {
		name       string
		d          coord.Doc
		state      string
		hours      map[string]int64
		want       string
		wantCount  bool
		wantDetail string
	}{
		{"breached", task("urgent", "todo", 11*time.Hour), opscoord.StateReady, hours, opstask.SLABreached, true, "SLA breached (urgent, 10h)"},
		{"at risk", task("urgent", "todo", 9*time.Hour), opscoord.StateReady, hours, opstask.SLAAtRisk, true, "SLA at risk (urgent, 10h)"},
		{"ok", task("urgent", "todo", time.Hour), opscoord.StateReady, hours, opstask.SLAOK, true, ""},
		{"priority without SLA", task("low", "todo", 99*time.Hour), opscoord.StateReady, hours, opstask.SLANone, false, ""},
		{"no policy", task("urgent", "todo", 99*time.Hour), opscoord.StateReady, nil, opstask.SLANone, false, ""},
		{"done row: counted, no late detail", task("urgent", "done", 11*time.Hour), opscoord.StateDone, hours, opstask.SLABreached, true, ""},
	}
	for _, c := range cases {
		r := opscoord.MissionRow{ID: "tsk_x", State: c.state}
		counted := withMissionSLA(&r, c.d, c.hours, now)
		if counted != c.wantCount || r.SLA != c.want || r.Detail != c.wantDetail {
			t.Errorf("%s: counted=%v sla=%q detail=%q, want %v %q %q", c.name, counted, r.SLA, r.Detail, c.wantCount, c.want, c.wantDetail)
		}
	}
}

// TestInboxWatchedAndAssignedE2E (later.md 488, 630): over real rows and
// ops, a watcher is told when someone else changes a watched field (not
// about its own change), and bob making me lead is an "assigned" item that
// names bob.
func TestInboxWatchedAndAssignedE2E(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t)
	out, err := run(t, f.me, opstask.OpAdd, `{"title":"rotate keys"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal([]byte(out), &shown); err != nil || shown.Data.ID == "" {
		t.Fatalf("task add: %s %v", out, err)
	}
	id := shown.Data.ID
	if _, err = run(t, f.me, opstask.OpWatch, `{"id":"`+id+`"}`, true); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, f.me, opstask.OpEdit, `{"id":"`+id+`","status":"in_progress"}`, true); err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(inboxItems(t, f.me)); got[inboxWatched] != 0 {
		t.Fatalf("my own change is not news to me: %v", got)
	}
	if _, err = run(t, f.bob, opstask.OpEdit, `{"id":"`+id+`","status":"blocked"}`, true); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, f.bob, opstask.OpEdit, `{"id":"`+id+`","status":"todo","assign":["`+f.meID+`"]}`, true); err != nil {
		t.Fatal(err)
	}
	var watched, assigned []opscoord.InboxItem
	for _, it := range inboxItems(t, f.me) {
		switch it.Kind {
		case inboxWatched:
			watched = append(watched, it)
		case inboxAssigned:
			assigned = append(assigned, it)
		}
	}
	if len(watched) == 0 {
		t.Fatal("bob's changes to a task I watch reach my inbox")
	}
	for _, it := range watched {
		if it.Who != "@bob" || !strings.Contains(it.Text, id) {
			t.Fatalf("a watched item names bob and the task: %+v", it)
		}
	}
	if !slices.ContainsFunc(watched, func(it opscoord.InboxItem) bool { return strings.Contains(it.Text, "status in_progress → blocked") }) {
		t.Fatalf("the status change is one of them: %+v", watched)
	}
	if len(assigned) != 1 || assigned[0].Who != "@bob" || !strings.HasSuffix(assigned[0].Text, ": made you lead") {
		t.Fatalf("bob made me lead: %+v", assigned)
	}
}

// kindsOf counts items per kind.
func kindsOf(items []opscoord.InboxItem) map[string]int {
	out := map[string]int{}
	for _, it := range items {
		out[it.Kind]++
	}
	return out
}

// TestInboxTeamAssignedAndRemovedTeam (G-15): a task assigned to a team I
// am on reaches my inbox as "(team @web)", as task list --mine lists it;
// --assign to a removed team is refused naming the team list and the
// active team with the same members.
func TestInboxTeamAssignedAndRemovedTeam(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t)
	ctx := background()
	webID, _, err := f.me.RosterTeamPut(ctx, store.SourceRepo, "", "web", "Web", []string{f.meID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, f.bob, opstask.OpAdd, `{"title":"fix login redirect","assign":["@web"]}`, true); err != nil {
		t.Fatal(err)
	}
	var team []opscoord.InboxItem
	for _, it := range inboxItems(t, f.me) {
		if it.Kind == inboxAssigned {
			team = append(team, it)
		}
	}
	if len(team) != 1 || !strings.Contains(team[0].Text, "fix login redirect (team @web)") {
		t.Fatalf("the team task in my inbox: %+v", team)
	}
	if _, _, err = f.me.RosterTeamPut(ctx, store.SourceRepo, "", "frontend", "Frontend", []string{f.meID}); err != nil {
		t.Fatal(err)
	}
	eng, err := f.me.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wid, err := ids.ParseAny(webID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eng.Update(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TableTeams, ID: wid}, map[string]value.Value{"active": value.BoolOf(false)}); err != nil {
		t.Fatal(err)
	}
	_, err = run(t, f.bob, opstask.OpAdd, `{"title":"web again","assign":["@web"]}`, true)
	if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "team @web was removed") {
		t.Fatalf("assign to a removed team: %v", err)
	}
	var e *errs.Error
	if !errors.As(err, &e) || !strings.Contains(e.Hint, "@frontend has the same members") || !strings.Contains(e.Hint, "lore team list") {
		t.Fatalf("hint of %v", err)
	}
	if got := kindsOf(inboxItems(t, f.me)); got[inboxAssigned] != 0 {
		t.Fatalf("a removed team's task is no one's assignment: %v", got)
	}
}
