package wire

// actorhome_test.go — devco D-486: a write into one folder of a store
// copied the caller's actor row into that folder although a sibling
// folder of the same store (the roster's, org/) held it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestFolderWriteKeepsTheActorRowInItsSibling: the first write into org/
// puts the caller's actor row there; a later write into acme/ of the same
// store finds it there and writes no copy into acme/actors, and doctor's
// roster check counts the sibling's row for acme/.
func TestFolderWriteKeepsTheActorRowInItsSibling(t *testing.T) {
	t.Parallel()
	repo, storeDir, yamlPath, yaml := folderStoreRepo(t)
	for _, f := range []string{"org", "acme"} {
		if err := os.MkdirAll(filepath.Join(storeDir, f), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(storeDir, f, rowfs.MetaFileName), []byte(`{"_v":1}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		yaml += "  - name: northwind/" + f + "\n    url: file://" + filepath.ToSlash(storeDir) + "\n    folder: " + f + "\n"
	}
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"company note","to":"northwind/org"}`, true); err != nil {
		t.Fatalf("write into org/: %v", err)
	}
	st, err := a.state(background())
	if err != nil {
		t.Fatal(err)
	}
	if held, herr := rosterHolds(background(), st, "northwind/org", st.actor); herr != nil || !held {
		t.Fatalf("org/ does not hold the caller's actor row after its first write (the setup under test): %v", herr)
	}
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"product note","to":"northwind/acme"}`, true); err != nil {
		t.Fatalf("write into acme/: %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(storeDir, "acme", string(schema.TableActors))); len(ents) != 0 {
		t.Fatalf("acme/ got %d actor row file(s) though org/ holds the row: %v", len(ents), ents)
	}
	if held, herr := rosterHolds(background(), st, store.Source("northwind/acme"), st.actor); herr != nil || !held {
		t.Fatalf("doctor's roster check does not count org/'s row for acme/: held %v, %v", held, herr)
	}
}
