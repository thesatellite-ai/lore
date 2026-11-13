package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestServingAppFollowsAStoreSetURL (W-3): a long-running `lore api` App
// opened its stores once. The store's repo is renamed and a CLI `lore
// store set-url` moves it (the old clone was fully pushed, so set-url
// clones the new address into a new folder); the server's next request
// opens the store there, so its writes land in the new clone, never in the
// old one whose origin is gone.
func TestServingAppFollowsAStoreSetURL(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	oldURL, oldDir := bareStore(t, hosts, "acme/kb")
	home := t.TempDir()
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores: []\n")
	cli := openWith(t, repo, Options{Home: home})
	withProject(t, cli)
	if _, err := run(t, cli, opsstores.OpInit, `{"name":"kb","url":"`+oldURL+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	_ = cli.Close()

	srv := openWith(t, repo, Options{Home: home})
	srv.ServeAsPerson()
	t.Cleanup(func() { _ = srv.Close() })
	histAddMemory(t, srv, "kb", "written through the api")
	if _, err := srv.SyncStores(background(), "kb"); err != nil {
		t.Fatalf("push: %v", err)
	}

	newURL := "https://" + histHost + "/acme/kb-moved"
	if err := os.Rename(oldDir, filepath.Join(hosts, "acme", "kb-moved")); err != nil {
		t.Fatal(err)
	}
	cli = openWith(t, repo, Options{Home: home})
	if _, err := run(t, cli, opsstores.OpSetURL, `{"name":"kb","url":"`+newURL+`"}`, true); err != nil {
		t.Fatalf("set-url: %v", err)
	}
	_ = cli.Close()
	newClone, _, err := gitbackend.CloneDirOf(home, newURL, "")
	if err != nil {
		t.Fatal(err)
	}
	oldClone, _, _ := gitbackend.CloneDirOf(home, oldURL, "")
	if newClone == oldClone {
		t.Fatalf("set-url kept the clone folder %s; this test needs a new one", newClone)
	}

	id := histAddMemory(t, srv, "kb", "written through the api after the move")
	rel := schema.TableMemories + "/" + id + ".json"
	if !strings.Contains(gitIn(t, newClone, nil, "ls-tree", "-r", "--name-only", "HEAD"), rel) {
		t.Fatalf("the server wrote to the old clone %s, not the store's clone %s at its new address", oldClone, newClone)
	}
}
