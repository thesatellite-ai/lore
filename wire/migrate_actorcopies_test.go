package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMigrateActorCopies: an actor copy written by lore 0.1.17 under the
// email's local part ("una") is stale against the 0.1.18 identity (git's
// user.name, here the configured name): a dry run names it and writes
// nothing, the run rewrites it from its source so doctor's actor_copies
// finds nothing, and a second run prints nothing.
func TestMigrateActorCopies(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	repo := gitRepo(t)
	home := srcHome(t, "identity:\n  key: human:una@example.test\n  name: Una Upgrader\n")
	a, err := New(Options{RepoDir: repo, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := background()
	withProject(t, a)
	if err = a.EnsureActor(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref := engine.Ref{Store: store.SourceRepo, Table: schema.TableActors, ID: st.actor.Any()}
	old, err := textFields(map[string]string{actorFieldDisplay: "una"}) // what 0.1.17 wrote
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.eng.Update(ctx, ref, old); err != nil {
		t.Fatal(err)
	}
	if f, err := a.DoctorActorCopies(ctx); err != nil || len(f) != 1 {
		t.Fatalf("before migrate: doctor actor_copies %v %v, want the one stale copy", f, err)
	}

	dry, n, err := a.migrateActorCopies(ctx, true)
	if err != nil || n != 1 || len(dry) != 1 || !strings.Contains(dry[0], "would refresh 1 actor copy") || !strings.Contains(dry[0], "Una Upgrader") {
		t.Fatalf("dry run: %q %d %v", dry, n, err)
	}
	if f, _ := a.DoctorActorCopies(ctx); len(f) != 1 {
		t.Fatalf("a dry run refreshed the copy: %v", f)
	}

	steps, n, err := a.migrateActorCopies(ctx, false)
	if err != nil || n != 1 || len(steps) != 1 || !strings.HasPrefix(steps[0], StepActorCopies+": refreshed 1 actor copy") {
		t.Fatalf("run: %q %d %v", steps, n, err)
	}
	if f, err := a.DoctorActorCopies(ctx); err != nil || len(f) != 0 {
		t.Fatalf("after migrate: doctor actor_copies still flags %v %v", f, err)
	}
	_, r, err := st.eng.Get(ctx, ref)
	if err != nil || fieldText(r, actorFieldDisplay) != "Una Upgrader" {
		t.Fatalf("the repo copy reads %q %v, want Una Upgrader", fieldText(r, actorFieldDisplay), err)
	}

	again, n, err := a.migrateActorCopies(ctx, false)
	if err != nil || n != 0 || len(again) != 0 {
		t.Fatalf("second run: %q %d %v, want nothing", again, n, err)
	}
}
