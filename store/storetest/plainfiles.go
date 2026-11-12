package storetest

// plainfiles.go — the STORE layout contract (2026-11-12, "plain files, no
// hidden magic"): a backend whose local copy is a folder a person opens
// and browses (a git store's clone) keeps every file of its own (the
// lock, the pending audit journal, stamps) in the folders it names as its
// own state (a clone's .git), never among the files a person reads. The
// check runs only for a fixture that implements PlainFiles.
//
//	write + audited write (journal pending) + a second handle's write
//	   ─▶ every file new outside the state folders is store data the
//	      backend committed (Tracked); the pending journal is inside them

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// PlainFiles is the optional Fixture upgrade of a backend whose local copy
// is a folder a person browses.
type PlainFiles interface {
	// Files returns that folder and the names of its sub-folders that
	// hold the backend's own state (".git" for a git clone).
	Files(t *testing.T) (dir string, state []string)
	// Tracked reports whether the file rel (slash separated, under the
	// folder) is store data the backend committed.
	Tracked(t *testing.T, rel string) bool
}

// testPlainFiles is the layout contract above.
func testPlainFiles(t *testing.T, f Fixture) {
	pf, ok := f.(PlainFiles)
	if !ok {
		t.Logf("%T has no browsable local folder (PlainFiles): nothing to check", f)
		return
	}
	b, g, ctx := open(t, f, store.OpenOptions{}), newGen(t), context.Background()
	dir, state := pf.Files(t)
	before := plainFiles(t, dir, state)
	a, c := g.row(newID(t), 1, hlc.HLC{}), g.row(newID(t), 1, hlc.HLC{})
	line := []byte(`{"ev":"plain files ✓"}`)
	if j, isJ := b.(store.AuditJournal); isJ {
		if err := j.WriteAudited(ctx, []store.Change{{Row: a}}, []store.AuditRecord{{Key: a.Key(), HLC: a.HLC, Line: line}}); err != nil {
			t.Fatalf("WriteAudited: %v", err)
		}
	} else {
		write(t, b, store.Change{Row: a})
		line = nil
	}
	second := open(t, f, store.OpenOptions{})
	write(t, second, store.Change{Row: c})
	for p := range plainFiles(t, dir, state) {
		if before[p] {
			continue
		}
		if !pf.Tracked(t, p) {
			t.Errorf("%s appeared among the store's browsable files but is no store data the backend committed: the backend's own files belong in %v", p, state)
		}
	}
	if line == nil {
		return
	}
	found := false
	for _, s := range state {
		root := filepath.Join(dir, s)
		if _, err := os.Stat(root); err != nil {
			continue // a state folder the backend has not made
		}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || found {
				return nil
			}
			if doc, rerr := os.ReadFile(p); rerr == nil && bytes.Contains(doc, line) {
				found = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Errorf("the pending audit journal is not inside the state folders %v", state)
	}
}

// plainFiles lists the files under dir outside the state folders,
// relative and slash separated.
func plainFiles(t *testing.T, dir string, state []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if slices.Contains(state, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		out[rel] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// StoreLayout is the optional Fixture upgrade of a backend whose local
// copy is a store checkout (a git store's clone, a folder store): a store
// keeps a folder's rows right under the folder (plain-files round, brief
// 1.2), never in a .lore/data inside the store.
type StoreLayout interface {
	// Layout returns the store's top on disk and the folder the fixture
	// opens ("." for the store root).
	Layout(t *testing.T) (dir, folder string)
}

// storeLayoutLoreDir is the folder a code repo keeps lore's files in; a
// store has none (a store's files are lore's already).
const storeLayoutLoreDir = ".lore"

// storeLayoutRootDir is where a store keeps its root folder's rows, the
// store's own rows (D-ad, rowfs.StoreDataDir; spelled here because the
// contract suite sits below the adapters).
const storeLayoutRootDir = "_store"

// testStoreLayout: a write lands at <folder>/<table>/<id>.json with the
// row's exact bytes (the store root folder's at _store/<table>/<id>.json,
// never in a table folder at the store's top), and no .lore folder
// appears anywhere in the store.
func testStoreLayout(t *testing.T, f Fixture) {
	sl, ok := f.(StoreLayout)
	if !ok {
		t.Logf("%T is no store checkout (StoreLayout): nothing to check", f)
		return
	}
	b, g := open(t, f, store.OpenOptions{}), newGen(t)
	dir, folder := sl.Layout(t)
	r := g.row(newID(t), 1, hlc.HLC{})
	write(t, b, store.Change{Row: r})
	data := folder
	if folder == "." || folder == "" {
		data = storeLayoutRootDir
	}
	want := filepath.Join(dir, filepath.FromSlash(data), r.Table.String(), r.ID.String()+".json")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("the row is not at <folder>/<table>/<id>.json (%s; the store root folder's in %s/): %v", want, storeLayoutRootDir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, r.Table.String())); err == nil {
		t.Errorf("%s: a table folder at the store's top (the root folder's rows sit in %s/)", filepath.Join(dir, r.Table.String()), storeLayoutRootDir)
	}
	if !bytes.Equal(got, r.Doc) {
		t.Fatalf("%s holds other bytes than the row written", want)
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() == storeLayoutLoreDir {
			t.Errorf("%s: a store keeps no .lore folder (rows sit right under each folder)", p)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
