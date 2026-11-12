package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestLayoutsOnDisk: one write per folder in each layout lands where the
// layout says. A store keeps rows right under each folder
// (<folder>/<table>/<id>.json, the root folder's in _store/) and
// _meta.json beside them; a code repo keeps .lore/data. Each folder of a
// store reads only its own tables: the root does not see org's rows, team
// does not see team/web's.
func TestLayoutsOnDisk(t *testing.T) {
	t.Parallel()
	g, ctx := newGen(t), context.Background()
	cases := []struct {
		layout          Layout
		root, org, deep string // root-relative row folder of ".", "org", "team/web"
		meta            string
	}{
		{LayoutStore, "_store/memories", "org/memories", "team/web/memories", "org/_meta.json"},
		{LayoutRepo, ".lore/data/memories", "org/.lore/data/memories", "team/web/.lore/data/memories", "org/.lore/data/_meta.json"},
	}
	for _, c := range cases {
		t.Run(c.layout.String(), func(t *testing.T) {
			r := openRoot(t, WithLayout(c.layout))
			if r.Layout() != c.layout {
				t.Fatalf("Layout() = %v", r.Layout())
			}
			want := map[string]string{FolderRoot: c.root, FolderOrg: c.org, "team/web": c.deep}
			rows := map[string]store.Row{}
			for _, name := range []string{FolderRoot, FolderOrg, "team", "team/web"} {
				f := folder(t, r, name)
				a := g.row(newID(t), "row of "+name, hlc.HLC{})
				rows[name] = a
				applied := apply(t, f, store.Change{Row: a})
				if dir, ok := want[name]; ok {
					p := dir + "/" + a.ID.String() + RowFileExt
					if !slices.Contains(applied.Paths, p) {
						t.Errorf("%s: written %v, want %s", name, applied.Paths, p)
					}
					if _, err := os.Stat(filepath.Join(r.Dir(), filepath.FromSlash(p))); err != nil {
						t.Errorf("%s: %v", name, err)
					}
					if got, rel, ok := c.layout.SplitRowPath(p); !ok || got != name || rel != RowPath(memories, a.ID) {
						t.Errorf("SplitRowPath(%s) = %q %q %v, want %q", p, got, rel, ok, name)
					}
					if rel, ok := f.FileRel(p); !ok || rel != RowPath(memories, a.ID) {
						t.Errorf("FileRel(%s) = %q %v", p, rel, ok)
					}
				}
			}
			if _, err := os.Stat(filepath.Join(r.Dir(), filepath.FromSlash(c.meta))); err != nil {
				t.Errorf("org's _meta.json: %v", err)
			}
			if f, n, ok := c.layout.SplitFolderFile(c.meta); !ok || f != FolderOrg || n != MetaFileName {
				t.Errorf("SplitFolderFile(%s) = %q %q %v", c.meta, f, n, ok)
			}
			for _, name := range []string{FolderRoot, "team"} {
				got, _, err := folder(t, r, name).Read(ctx, "")
				if err != nil || len(got) != 1 || got[0].ID != rows[name].ID {
					t.Errorf("%s reads %v %v, want only its own row", name, got, err)
				}
				probs, err := folder(t, r, name).Problems(ctx)
				if err != nil || len(probs) != 0 {
					t.Errorf("%s problems %v %v: the folders below are not problems", name, probs, err)
				}
			}
			old, err := IsOldStoreLayout(r.Dir())
			if err != nil || old != (c.layout == LayoutRepo) {
				t.Errorf("IsOldStoreLayout = %v %v", old, err)
			}
		})
	}
}

// TestLayoutPaths: the path helpers of both layouts, and that a store path
// whose folder part uses a table name, or that is not <table>/<id>.json,
// is no row path.
func TestLayoutPaths(t *testing.T) {
	t.Parallel()
	id := newID(t)
	file := id.String() + RowFileExt
	if got := LayoutStore.DataPath(FolderRoot); got != StoreDataDir {
		t.Errorf("store root data path %q", got)
	}
	if got := LayoutRepo.DataPath(FolderRoot); got != ".lore/data" {
		t.Errorf("repo data path %q", got)
	}
	if got := LayoutStore.StorePath(FolderRoot, RowPath(memories, id)); got != "_store/memories/"+file {
		t.Errorf("store root row path %q", got)
	}
	// D-ad: the root folder's rows sit in _store/, its folder files too
	if f, rel, ok := LayoutStore.SplitRowPath("_store/memories/" + file); !ok || f != FolderRoot || rel != "memories/"+file {
		t.Errorf("store SplitRowPath(_store/…) = %q %q %v", f, rel, ok)
	}
	if f, name, ok := LayoutStore.SplitFolderFile("_store/" + MetaFileName); !ok || f != FolderRoot || name != MetaFileName {
		t.Errorf("store SplitFolderFile(_store/_meta.json) = %q %q %v", f, name, ok)
	}
	if f, _, ok := LayoutStore.SplitFolderFile("org/" + MetaFileName); !ok || f != "org" {
		t.Errorf("store SplitFolderFile(org/_meta.json) = %q %v", f, ok)
	}
	if _, _, ok := LayoutStore.SplitFolderFile(MetaFileName); ok {
		t.Error("store SplitFolderFile took a _meta.json at the top (the first plain layout's place)")
	}
	if specs := LayoutStore.Pathspecs(FolderRoot); !slices.Contains(specs, "_store/tasks") || slices.Contains(specs, "tasks") {
		t.Errorf("store root pathspecs %v", specs)
	}
	for _, bad := range []string{file, "memories/" + file, "_store/org/memories/" + file, "_other/memories/" + file, "notes/" + file, "tasks/memories/" + file, "org/memories/x.json", "org/README.md", ".lore/data/memories/" + file} {
		if f, rel, ok := LayoutStore.SplitRowPath(bad); ok {
			t.Errorf("store SplitRowPath(%s) = %q %q, want no row", bad, f, rel)
		}
	}
	if _, _, ok := LayoutRepo.SplitRowPath("memories/" + file); ok {
		t.Error("repo SplitRowPath took a path outside .lore/data")
	}
	specs := LayoutStore.Pathspecs("team")
	if !slices.Contains(specs, "team/tasks") || !slices.Contains(specs, "team/memories") || slices.Contains(specs, "team") {
		t.Errorf("store pathspecs %v", specs)
	}
	if specs := LayoutRepo.Pathspecs(FolderRoot); !slices.Equal(specs, []string{".lore/data"}) {
		t.Errorf("repo pathspecs %v", specs)
	}
	for p, want := range map[string]bool{".lore/data": true, ".lore/data/tasks/x.json": true, "org/.lore/data/_meta.json": true, "org/tasks/x.json": false, ".lore-state/lock": false, "a.lore/data/x": false} {
		if OldLayoutPath(p) != want {
			t.Errorf("OldLayoutPath(%s) = %v", p, !want)
		}
	}
}

// TestFlatRootLeft: a store whose top still holds a table folder or the
// root's _meta.json / _purged.json (the first plain layout, lore_store: 3)
// needs the move into _store/; one with only _store/ and project folders
// does not.
func TestFlatRootLeft(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		dirs, files []string
		left        bool
	}{
		"moved":        {[]string{"_store/tasks", "acme/tasks"}, []string{"_store/_meta.json", "acme/_meta.json", "README.md"}, false},
		"table at top": {[]string{"acme/tasks"}, []string{"tasks/tsk_01a1a5aca7f57659be7320c5f4b70033.json"}, true},
		"stray only":   {[]string{"tasks"}, []string{"tasks/notes.txt"}, false},
		"meta at top":  {[]string{"_store/tasks"}, []string{MetaFileName}, true},
		"purged":       {nil, []string{PurgedFileName}, true},
		"empty":        {nil, nil, false},
	} {
		top := t.TempDir()
		for _, d := range c.dirs {
			if err := os.MkdirAll(filepath.Join(top, filepath.FromSlash(d)), 0o750); err != nil {
				t.Fatal(err)
			}
		}
		for _, f := range c.files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(top, filepath.FromSlash(f))), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(top, filepath.FromSlash(f)), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := FlatRootLeft(top); got != c.left {
			t.Errorf("%s: FlatRootLeft = %v, want %v", name, got, c.left)
		}
	}
}

// TestIsOldStoreLayout: a store tree with .lore/data at its top or in any
// folder is in the old layout; a flat one, or one whose only .lore is in
// .git or another dot folder, is not.
func TestIsOldStoreLayout(t *testing.T) {
	t.Parallel()
	mk := func(top string, dirs ...string) string {
		t.Helper()
		for _, d := range dirs {
			if err := os.MkdirAll(filepath.Join(top, filepath.FromSlash(d)), 0o750); err != nil {
				t.Fatal(err)
			}
		}
		return top
	}
	for name, c := range map[string]struct {
		dirs []string
		old  bool
	}{
		"flat":        {[]string{"tasks", "org/memories", "team/web/tasks"}, false},
		"old root":    {[]string{".lore/data/tasks"}, true},
		"old folder":  {[]string{"tasks", "client_a/.lore/data/tasks"}, true},
		"old nested":  {[]string{"team/web/.lore/data"}, true},
		"dot folders": {[]string{".git/x/.lore/data", ".lore-state", "tasks/.lore/data", "_store/.lore/data"}, false},
	} {
		got, err := IsOldStoreLayout(mk(t.TempDir(), c.dirs...))
		if err != nil || got != c.old {
			t.Errorf("%s: IsOldStoreLayout = %v %v, want %v", name, got, err, c.old)
		}
	}
}

// TestOldLayoutFolder: a store folder still in the old layout
// (<folder>/.lore/data) is read and written there until it is moved
// (Folder.OldLayout); FindRow finds its rows; a new folder of the same
// store is flat.
func TestOldLayoutFolder(t *testing.T) {
	t.Parallel()
	g, ctx := newGen(t), context.Background()
	r := openRoot(t)
	oldRow := g.row(newID(t), "old", hlc.HLC{})
	p := filepath.Join(r.Dir(), "client", LoreDir, DataDir, filepath.FromSlash(RowPath(memories, oldRow.ID)))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, oldRow.Doc, 0o600); err != nil {
		t.Fatal(err)
	}
	f := folder(t, r, "client")
	if !f.OldLayout() || f.DataPath() != "client/.lore/data" {
		t.Fatalf("old folder: OldLayout %v, data %q", f.OldLayout(), f.DataPath())
	}
	got, _, err := f.Read(ctx, "")
	if err != nil || len(got) != 1 || got[0].ID != oldRow.ID {
		t.Fatalf("old folder reads %v %v", got, err)
	}
	if fd, found, ferr := r.FindRow(memories, oldRow.ID); ferr != nil || !found || fd != "client" {
		t.Fatalf("FindRow in an old folder: %q %v %v", fd, found, ferr)
	}
	fresh := folder(t, r, "fresh")
	if fresh.OldLayout() || fresh.DataPath() != "fresh" {
		t.Fatalf("new folder: OldLayout %v, data %q", fresh.OldLayout(), fresh.DataPath())
	}
	repo := folder(t, openRoot(t, WithLayout(LayoutRepo)), FolderRoot)
	if repo.OldLayout() {
		t.Fatal("a code repo's .lore/data is its layout, not an old one")
	}
}
