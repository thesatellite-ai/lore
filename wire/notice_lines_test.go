package wire

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/store"
)

// W-4: the store clone notices of an upgrade are one short line each.
func TestStoreCloneNoticesShort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clone := filepath.Join(home, ".lore", "stores", "acme", "lore-notes")
	set := []string{".git/config: merge.lore.name", ".git/config: merge.lore.driver", ".git/info/attributes: *.json merge=lore", ".git/hooks/pre-commit"}
	hand := handGitSetUpLine(context.Background(), clone, set)
	if !strings.HasSuffix(hand, "store clone ~/.lore/stores/acme/lore-notes: set up for plain git (merge driver, pre-commit hook; `lore where` lists them)") {
		t.Fatalf("hand git line %q", hand)
	}
	o := git.OldClone{V017: true, Bare: filepath.Join(home, ".lore", "clones", "github.com", "acme", "lore-notes.git")}
	moved := "moved store clone " + movedFrom(o) + " -> " + underHome(clone) + carriedText(git.MoveResult{Carried: 2, Files: []string{"a.json"}})
	if moved != "moved store clone ~/.lore/clones/github.com/acme/lore-notes.git (0.1.17) -> ~/.lore/stores/acme/lore-notes (kept 2 local commits, 1 local file)" {
		t.Fatalf("moved line %q", moved)
	}
	// two ~/.lore paths are most of a moved line; the worst case (both
	// kinds of local work carried) still fits one terminal line
	for _, l := range []string{hand, moved} {
		if len(l) > 150 {
			t.Errorf("%d chars: %q", len(l), l)
		}
	}
}

// W-4: a push that did not reach a store with no repository behind it
// says so (git's reason, like the read path); no answer stays
// "unreachable".
func TestStorePushOfflineWhy(t *testing.T) {
	b := &storePush{name: "me"}
	gone := "fatal: '/srv/none/lore-me.git' does not appear to be a git repository\nfatal: Could not read from remote repository.\n"
	if got := b.offlineWhy(store.SyncReport{Offline: true, OfflineDetail: gone}); !strings.HasPrefix(got, "no git repository at me ('/srv/none/lore-me.git' does not appear to be a git repository); check the store's address") {
		t.Fatalf("missing repo: %q", got)
	}
	dns := "ssh: Could not resolve hostname git.example.invalid: Name or service not known\n"
	if got := b.offlineWhy(store.SyncReport{Offline: true, OfflineDetail: dns}); got != "the store is unreachable" {
		t.Fatalf("no network: %q", got)
	}
	if got := b.offlineWhy(store.SyncReport{Offline: true}); got != "the store is unreachable" {
		t.Fatalf("no detail: %q", got)
	}
}
