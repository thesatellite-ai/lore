package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestWithExtraKeepsTheHeldStateOpen: a query with extra folders attached
// (withExtra) runs while a write already holds the App's engine, as the
// leak guard of a client repo does inside Create. The held engine must
// still write afterwards and the App must hand back that same engine:
// closing it under the write made `lore rule add --to repo` fail
// E_INTERNAL "git: closed" (H-176, devco:D-918).
func TestWithExtraKeepsTheHeldStateOpen(t *testing.T) {
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	held, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	extra := []attach.Store{{Name: store.Source("acme/web"), URL: "git@example.invalid:acme/lore-team.git", Folder: "web", ReadOnly: true}}
	ran := false
	if err := a.withExtra(extra, func() error { ran = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("withExtra did not run its query")
	}
	body, err := value.TextOf("written after the extra query")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := held.Create(background(), store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": body}); err != nil {
		t.Fatalf("the engine held across withExtra no longer writes: %v", err)
	}
	again, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	if again != held {
		t.Fatal("withExtra must put the held state back, not reopen another")
	}
	a.mu.Lock()
	n := len(a.extra)
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("withExtra left %d extra stores attached", n)
	}
}
