package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// withFolderStores makes one folder store per name (fresh sto_ ids) and
// attaches them all at their root through the repo's stores.yaml.
func withFolderStores(t *testing.T, repo string, names ...string) map[string]string {
	t.Helper()
	dirs := map[string]string{}
	yaml := "stores:\n"
	for _, n := range names {
		dir := t.TempDir()
		sid, err := ids.New[ids.Store]()
		if err != nil {
			t.Fatal(err)
		}
		if err = folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
			t.Fatal(err)
		}
		dirs[n] = dir
		yaml += "  - name: " + n + "\n    url: file://" + filepath.ToSlash(dir) + "\n"
	}
	if err := os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(attach.FileName)), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return dirs
}

// TestGroupRepoID: --repo for a store target finds or creates the repos
// row in that store's project (created once, the same id on a second call
// and from a fresh App), two stores keep separate rows, and the repo
// store keeps RepoID's rule (an unknown name is E_NOT_FOUND, nothing made).
func TestGroupRepoID(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	dirs := withFolderStores(t, repo, "northwind", "acme")
	a := newTestApp(t, repo)
	withProject(t, a)
	ctx := background()

	first, err := a.GroupRepoID(ctx, "northwind", "backend")
	if err != nil {
		t.Fatal(err)
	}
	if first.Prefix() != ids.PrefixRepo {
		t.Fatalf("not a repo id: %s", first)
	}
	again, err := a.GroupRepoID(ctx, "northwind", "backend")
	if err != nil || again != first {
		t.Fatalf("second call: %s %v, want %s", again, err, first)
	}
	byID, err := a.GroupRepoID(ctx, "northwind", first.String())
	if err != nil || byID != first {
		t.Fatalf("by id: %s %v", byID, err)
	}
	other, err := a.GroupRepoID(ctx, "acme", "backend")
	if err != nil || other == first {
		t.Fatalf("acme must keep its own row: %s %v (northwind %s)", other, err, first)
	}
	for name, dir := range dirs {
		entries, rerr := os.ReadDir(filepath.Join(dir, rowfs.StoreDataDir, schema.TableRepos))
		if rerr != nil || len(entries) != 1 {
			t.Fatalf("%s: want exactly one repos row file, got %d %v", name, len(entries), rerr)
		}
	}
	if _, err = a.GroupRepoID(ctx, store.SourceRepo, "backend"); !errs.Has(err, errs.NotFound) {
		t.Fatalf("repo store, unknown repo: want E_NOT_FOUND, got %v", err)
	}
	if _, err = a.GroupRepoID(ctx, "", "backend"); !errs.Has(err, errs.NotFound) {
		t.Fatalf("default target, unknown repo: want E_NOT_FOUND, got %v", err)
	}
	if _, err = a.GroupRepoID(ctx, "northwind", " "); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("blank name: %v", err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	b := newTestApp(t, repo)
	if fresh, ferr := b.GroupRepoID(ctx, "northwind", "backend"); ferr != nil || fresh != first {
		t.Fatalf("a fresh App: %s %v, want %s", fresh, ferr, first)
	}
}
