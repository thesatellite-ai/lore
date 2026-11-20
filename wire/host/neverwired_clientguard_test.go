package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/wire"
)

// TestNeverWiredIgnoresTheClientGuard (devco:N-10): a client repo where
// `lore org link` installed only its guard (the commit-msg and pre-push
// blocks, lore.clientrepo set) was never set up for lore, so the first lore init there must not
// say the leak guard was off; the same block in a repo that is no client
// repo still counts as lore's wiring.
func TestNeverWiredIgnoresTheClientGuard(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	hooks := filepath.Join(dir, ".git", "hooks")
	// every block the client guard installs (commit-msg, and pre-push
	// since D-730): with pre-push counted as wiring, the first lore init
	// after the first link said the guard was off (D-733)
	for _, n := range clientGuardHookNames {
		marker, lines := clientGuardBlock(n)
		if err := ensureHookBlock(hooks, n, marker, lines); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(hooks, n)); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &liveSession{repo: repo, top: dir}
	if s.neverWired(ctx, hooks) {
		t.Fatal("a lore commit-msg block in a repo that is no client repo must count as wired")
	}
	gitT(t, dir, "config", wire.ClientRepoGitKey, "true")
	if !s.neverWired(ctx, hooks) {
		t.Fatal("the client guard alone (lore.clientrepo=true) must not count as lore having wired the clone")
	}
	gitT(t, dir, "config", "merge."+mergeDriverLore+".driver", "lore merge-driver %O %A %B %P")
	if s.neverWired(ctx, hooks) {
		t.Fatal("a merge driver set by lore means the clone was wired")
	}
}
