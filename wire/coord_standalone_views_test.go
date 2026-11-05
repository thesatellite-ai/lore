package wire

// coord_standalone_views_test.go — lore inbox and lore mission status on
// cmd/lore's coordination host (coord_standalone_views.go) over real row
// files: later.md 212 (authors from the row envelope).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
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
