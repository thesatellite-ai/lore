package git

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestRefusalNamesItsFolder: one row edited on both sides is refused by
// every folder of the clone (the one merge carries them all), but only the
// folder that holds the row reports it as its own; another folder's
// report marks it held (owner decision 7). Rows in the store root carry
// rowfs.FolderRoot.
func TestRefusalNamesItsFolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote)
	base := filepath.Join(t.TempDir(), "clones", "host", "store")
	open := func(folder string) *Backend {
		b, err := OpenClone(ctx, remote, base, folder, store.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Close() })
		return b
	}
	root, org := open(rowfs.FolderRoot), open(rowfs.FolderOrg)
	ga, gb := newGen(t), newGen(t)
	x := ga.row(newID(t), "x", hlc.HLC{})
	write(t, a, store.Change{Row: x})
	syncOK(t, a)
	syncOK(t, root)
	write(t, a, store.Change{Row: ga.row(x.ID, "a side", x.HLC), Expect: x.HLC})
	syncOK(t, a)
	write(t, root, store.Change{Row: gb.row(x.ID, "b side", x.HLC), Expect: x.HLC})

	for _, c := range []struct {
		name string
		b    *Backend
		held bool
	}{{"the row's folder", root, false}, {"another folder", org, true}} {
		rep, err := c.b.Sync(ctx)
		if err != nil || len(rep.Refused) != 1 {
			t.Fatalf("%s: sync = %+v %v, want x refused", c.name, rep, err)
		}
		r := rep.Refused[0]
		if r.ID != x.ID || r.Code != errs.Stale || r.Folder != rowfs.FolderRoot || r.Held != c.held {
			t.Fatalf("%s: refusal %+v, want folder %q held %v", c.name, r, rowfs.FolderRoot, c.held)
		}
	}
}

// rowPlace names the folder of a row path: the root's data dir, a project
// folder, a nested folder; anything else is no row.
func TestRowPlace(t *testing.T) {
	t.Parallel()
	id := "mem_01a1c3f7f9e378678bbc3ab7e7fc04a8"
	cases := []struct {
		path, folder string
		ok           bool
	}{
		{"_store/memories/" + id + ".json", rowfs.FolderRoot, true},
		{"memories/" + id + ".json", "", false}, // the first plain layout's place: moved by the layout commit
		{"platform/memories/" + id + ".json", "platform", true},
		{"org/memories/" + id + ".json", "org", true},
		{"a/b/memories/" + id + ".json", "a/b", true},
		{"lore-store.yaml", "", false},
		{"platform/_meta.json", "", false},
		{"platform/.lore/data/memories/" + id + ".json", "platform", true}, // a folder not moved yet
	}
	for _, c := range cases {
		folder, rel, ok := rowPlace(c.path)
		if ok != c.ok || folder != c.folder || (ok && rel != "memories/"+id+".json") {
			t.Errorf("rowPlace(%q) = %q %q %v, want %q %v", c.path, folder, rel, ok, c.folder, c.ok)
		}
	}
}
