package wire

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// folderKeyRows returns the repo rows of a checkout holding one memory, as
// they are first ("pelican" text) and after an edit ("walrus" text).
func folderKeyRows(t *testing.T) (before, after []store.Row) {
	t.Helper()
	ctx := background()
	repo, home := gitRepo(t), t.TempDir()
	a := newTestAppHome(t, repo, home)
	withProject(t, a)
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := a.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{}
	for k, v := range map[string]string{"body": "pelican invoices are exported nightly", "kind": schema.MemoryKindCore, "source_kind": "test"} {
		if fields[k], err = value.TextOf(v); err != nil {
			t.Fatal(err)
		}
	}
	if fields["project_id"], err = value.RefOf(pid.Any()); err != nil {
		t.Fatal(err)
	}
	w, err := eng.Create(ctx, store.SourceRepo, schema.TableMemories, fields)
	if err != nil {
		t.Fatal(err)
	}
	if before, err = eng.CurrentRows(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	body, err := value.TextOf("walrus receipts are exported hourly")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Update(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TableMemories, ID: w.Row.ID}, map[string]value.Value{"body": body}); err != nil {
		t.Fatal(err)
	}
	if after, err = eng.CurrentRows(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	return before, after
}

// folderRouter opens the store folder file of a made-up store under home
// at two attach names (kb/app in one checkout, notes in another).
func folderRouter(t *testing.T, home string) (*caches, string) {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	c := newCachesFor(home)
	path := filepath.Join(writableHome(home), cacheDir, cacheStoresDir, "sto_acme", "main", "app"+cacheExt)
	if err := mkCacheDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []store.Source{"kb/app", "notes"} {
		if err := c.open(background(), s, path, reg, refs.Expander{}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, path
}

// TestFolderCacheKeepsOneCopyWhateverTheName (devco D-933, hunt H-194,
// public issue #142): a store folder's cache keys its rows by the folder,
// never by the attach name, so two checkouts attaching one folder as
// kb/app and as notes share ONE copy of each row: an edit read under one
// name replaces the copy the other name searches, with no rebuild. Before,
// the file held one copy per name and search, which reads the whole file,
// found the other name's old text.
func TestFolderCacheKeepsOneCopyWhateverTheName(t *testing.T) {
	t.Parallel()
	ctx := background()
	before, after := folderKeyRows(t)
	c, _ := folderRouter(t, t.TempDir())
	if err := c.Apply(ctx, "kb/app", before, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(ctx, "notes", after, ""); err != nil {
		t.Fatal(err)
	}
	f, err := c.of("kb/app")
	if err != nil {
		t.Fatal(err)
	}
	srcs, err := f.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 || !strings.HasPrefix(string(srcs[0]), folderKeyPrefix) || c.keyOf("kb/app") != c.keyOf("notes") {
		t.Fatalf("the folder's file keeps its rows under %v, want one folder key for both names", srcs)
	}
	old, err := c.search(ctx, schema.TableMemories, "pelican", 10, map[store.Source]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != 0 {
		t.Fatalf("search still finds the old text: %v", old)
	}
	hits, err := c.search(ctx, schema.TableMemories, "walrus", 10, map[store.Source]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Source != "kb/app" {
		t.Fatalf("search for the new text = %v, want one hit labelled with the first name", hits)
	}
}

// TestOpenDropsAnOlderLoresNameKeyedCopies (devco D-933): a store folder's
// file an older lore filled holds one copy per attach name; the first open
// keeps only the folder key's rows, so no name-keyed copy is searched.
func TestOpenDropsAnOlderLoresNameKeyedCopies(t *testing.T) {
	t.Parallel()
	ctx := background()
	before, _ := folderKeyRows(t)
	c, _ := folderRouter(t, t.TempDir())
	f, err := c.of("notes")
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []store.Source{"kb/app", "notes"} {
		if err := f.Apply(ctx, old, before, ""); err != nil { // the older lore's keys
			t.Fatal(err)
		}
	}
	if err := c.Apply(ctx, "notes", before, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.dropUnusedNames(ctx); err != nil {
		t.Fatal(err)
	}
	srcs, err := f.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(srcs, []store.Source{c.keyOf("notes")}) {
		t.Fatalf("after the open the file keeps rows under %v, want only %s", srcs, c.keyOf("notes"))
	}
}

// TestFolderKeyOnlyForStoreFolderFiles: the repo's own cache and every
// file outside <home>/cache/stores keep their rows under the name.
func TestFolderKeyOnlyForStoreFolderFiles(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	c := newCachesFor(home)
	root := filepath.Join(writableHome(home), cacheDir)
	for path, want := range map[string]store.Source{
		filepath.Join(root, cacheStoresDir, "sto_acme", "main", "app"+cacheExt):       "@sto_acme/main/app",
		filepath.Join(root, cacheStoresDir, "sto_acme", "main", "kb", "web"+cacheExt): "@sto_acme/main/kb/web",
		RepoCachePath(home, "/work/ledger"):                                           "",
		filepath.Join(root, combinedDir, "abc"+cacheExt):                              "",
		filepath.Join(root, cacheStoresDir+"x", "sto_acme", "main", "app"+cacheExt):   "",
	} {
		if got := c.folderKey(path); got != want {
			t.Errorf("folderKey(%s) = %q, want %q", path, got, want)
		}
	}
	if got := newCaches().folderKey(filepath.Join(root, cacheStoresDir, "a.db")); got != "" {
		t.Errorf("a router with no home keyed a file by folder: %q", got)
	}
}
