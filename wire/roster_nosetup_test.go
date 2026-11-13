package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/schema"
)

// TestRosterWriteInRepoWithNoLore pins R-12: a roster write (lore team
// invite --setup <admin>, member add) run from a git repo with no .lore/
// carries the host's no-set-up mark (S-2, WithNoSetUp), so the full open
// refuses that repo. The roster lives in the org store, so the store-only
// App serves the write, and the repo stays untouched (no .lore/, no
// .git/lore). Before, only a read went store-only and the write failed
// with E_NOT_A_STORE: the setup page's member re-join found no invite line.
func TestRosterWriteInRepoWithNoLore(t *testing.T) {
	t.Parallel()
	ctx := WithNoSetUp(background())
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
	}
	repo := gitRepo(t)
	a, ok, err := StoreOnlyRoster(ctx, Options{RepoDir: repo, Home: t.TempDir(), FlagActor: testEmail, OrgStores: hook, Wall: time.Now}, rosterStore)
	if err != nil || !ok {
		t.Fatalf("a write that may not set up, in a repo with no lore project: ok %v err %v, want the store-only App", ok, err)
	}
	t.Cleanup(func() { _ = a.Close() })
	id, created, err := a.RosterRegister(ctx, rosterStore, RosterActor{Kind: schema.ActorKindHuman, StableKey: "mailto:ana@example.com", Name: "Ana", Handle: "ana"})
	if err != nil || !created {
		t.Fatalf("roster write: %s %v %v", id, created, err)
	}
	if got, found := findActor(t, a, id); !found || got.Handle != "ana" {
		t.Fatalf("roster read back: %+v %v", got, found)
	}
	if loreFolderAt(repo) {
		t.Fatal("the roster write created .lore/ in the repo")
	}
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(strings.TrimSpace(string(out)), "lore")); !os.IsNotExist(err) {
		t.Fatalf("the roster write made .git/lore in the repo: %v", err)
	}
}
