package wire

// folderproject_agent_test.go — devco:D-694: an agent's first write into
// a team folder that had no project row yet (memory add --to
// <org>/<team folder>) failed E_NOT_FOUND "memories refers to rows no
// attached store holds: project_id". The command resolves the folder's
// project before its own write batch, so the project row was committed
// alone, stating the agent as its writer, while the agent's actor row did
// not exist anywhere yet; every reader's authority replay holds such a
// gated row back.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
)

// TestAgentFolderProjectRowFollowsTheAgentRow: when an agent session makes
// a folder's project row, the agent's actor row is in place by the time
// the project row is written, so the row's stated writer is on the roster.
func TestAgentFolderProjectRowFollowsTheAgentRow(t *testing.T) {
	t.Parallel()
	repo, storeDir, yamlPath, yaml := folderStoreRepo(t)
	for _, f := range []string{"org", "web"} {
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
	a, err := New(Options{RepoDir: repo, Home: t.TempDir(), FlagActor: agentKey, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	withProject(t, a)
	st, err := a.writeState(background())
	if err != nil {
		t.Fatal(err)
	}
	web := store.Source("northwind/web")
	if held, herr := rosterHolds(background(), st, web, st.actor); herr != nil || held {
		t.Fatalf("the agent's actor row is already on the roster before its first folder write (the setup under test): held %v, %v", held, herr)
	}
	if _, err := a.rosterProject(background(), st, web, "web"); err != nil {
		t.Fatalf("the web folder's project row: %v", err)
	}
	if held, herr := rosterHolds(background(), st, web, st.actor); herr != nil || !held {
		t.Fatalf("[devco:D-694] the web folder's project row was written stating the agent as its writer, but the agent's actor row is on no roster: held %v, %v", held, herr)
	}
}
