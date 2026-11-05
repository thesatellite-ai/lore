package wire

// actor_projection_test.go — later 385: the FLAG6 client actor projection
// is defined once in logic/actorproject; these tests pin wire's use of it
// (ensureNamedActors / writeClientActor) end to end in a client repo.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/actorproject"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestActorProjectionKindsMatchSchema: logic/actorproject names the closed
// actors.kind and column values itself (it imports model only); they must
// stay the schema's.
func TestActorProjectionKindsMatchSchema(t *testing.T) {
	if actorproject.KindAgent != schema.ActorKindAgent || actorproject.FieldKind != actorFieldKind || actorproject.FieldDisplayName != actorFieldDisplay {
		t.Fatal("logic/actorproject constants drifted from the schema")
	}
}

// TestClientRepoNamedActorIsTheProjection: a task in a client repo that
// names another person (bob, whose row in the attached internal store
// carries a stable key and a handle) writes bob's row into the repo as
// {kind, display_name} only; a later write after bob's name changed in the
// store updates the copy; an agent named while agent_details is internal
// gets no copy at all.
func TestClientRepoNamedActorIsTheProjection(t *testing.T) {
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+folderStoreAt(t, "sto_0e45506e64357c94a87e70ef1c6b0001", "internal")+"\n")
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return true, nil }})
	ctx := background()
	withProject(t, a)
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	put := func(id ids.ID[ids.Actor], vals map[string]string) {
		t.Helper()
		f, ferr := textFields(vals)
		if ferr != nil {
			t.Fatal(ferr)
		}
		if _, ferr = eng.CreateWithID(ctx, "kb", schema.TableActors, id.Any(), f); ferr != nil {
			t.Fatal(ferr)
		}
	}
	bob := mustActor(t, "act_0e45506e64357c94a87e70ef1c6b0b0b")
	agent := mustActor(t, "act_0e45506e64357c94a87e70ef1c6b0a9e")
	put(bob, map[string]string{actorFieldKind: schema.ActorKindHuman, actorFieldDisplay: "Bob Roe", actorFieldKey: "human:bob@example.com", actorFieldHandle: "bob"})
	put(agent, map[string]string{actorFieldKind: schema.ActorKindAgent, actorFieldDisplay: "bob via codex", actorFieldKey: "agent:codex:bob"})

	file := func(id ids.ID[ids.Actor]) string {
		return filepath.Join(repo, ".lore", "data", schema.TableActors, id.String()+".json")
	}
	if _, err = run(t, a, opstask.OpAdd, `{"title":"check the phone layout","assigned_to":"`+bob.String()+`"}`, true); err != nil {
		t.Fatal(err)
	}
	f := rowFile(t, file(bob))
	if string(f[actorFieldDisplay]) != `"Bob Roe"` || string(f[actorFieldKind]) != `"`+schema.ActorKindHuman+`"` {
		t.Fatalf("bob's client copy = %v", f)
	}
	for _, k := range []string{actorFieldKey, actorFieldHandle} {
		if _, has := f[k]; has {
			t.Fatalf("bob's client copy carries %s: %v", k, f)
		}
	}
	if strings.Contains(string(mustRead(t, file(bob))), "bob@example.com") {
		t.Fatal("bob's email reached the client repo")
	}

	// bob renamed in the store: the next row naming him updates the copy
	t2, err := value.TextOf("Bob R.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eng.Update(ctx, engineRefStore("kb", bob), map[string]value.Value{actorFieldDisplay: t2}); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, a, opstask.OpAdd, `{"title":"second","assigned_to":"`+bob.String()+`"}`, true); err != nil {
		t.Fatal(err)
	}
	if f = rowFile(t, file(bob)); string(f[actorFieldDisplay]) != `"Bob R."` {
		t.Fatalf("bob's client copy after the rename = %v", f)
	}

	// an agent while agent_details is internal (the default): no copy
	_, _ = run(t, a, opstask.OpAdd, `{"title":"agent work","assigned_to":"`+agent.String()+`"}`, true)
	if _, serr := os.Stat(file(agent)); !os.IsNotExist(serr) {
		t.Fatalf("an agent's row reached the client repo: %v", serr)
	}
}

func mustActor(t *testing.T, s string) ids.ID[ids.Actor] {
	t.Helper()
	id, err := ids.Parse[ids.Actor](s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// engineRefStore names an actors row in src.
func engineRefStore(src store.Source, id ids.ID[ids.Actor]) engine.Ref {
	return engine.Ref{Store: src, Table: schema.TableActors, ID: id.Any()}
}
