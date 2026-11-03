package main

import (
	"testing"

	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/stores"
)

// Two creators of one store folder must derive the same project id, and any
// difference in store, branch or folder must give a different one.
func TestStoreFolderProjectIDIsOnePerFolder(t *testing.T) {
	base := storeFolderKey{Store: "github.com/northwind/lore", Branch: "main", Folder: "org"}
	first, err := storeFolderProjectID(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := ids.Validate(first, ids.PrefixProject); err != nil {
		t.Fatalf("not a project id: %v", err)
	}
	again, err := storeFolderProjectID(base)
	if err != nil || again != first {
		t.Fatalf("same folder, two ids: %s vs %s (%v)", first, again, err)
	}
	for name, k := range map[string]storeFolderKey{
		"other store":  {Store: "github.com/betalabs/lore", Branch: "main", Folder: "org"},
		"other branch": {Store: base.Store, Branch: "staging", Folder: "org"},
		"other folder": {Store: base.Store, Branch: "main", Folder: "acme"},
		"root folder":  {Store: base.Store, Branch: "main", Folder: stores.RootFolder},
		// The parts are hashed with a separator: moving a character from
		// one part to the next must not collide.
		"shifted parts": {Store: base.Store + "m", Branch: "ain", Folder: "org"},
	} {
		id, err := storeFolderProjectID(k)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if id == first {
			t.Errorf("%s: shares the project id of %+v", name, base)
		}
	}
	// No shared identity (the local personal store): random, one writer.
	a, _ := storeFolderProjectID(storeFolderKey{Folder: "notes"})
	b, _ := storeFolderProjectID(storeFolderKey{Folder: "notes"})
	if a == b {
		t.Error("a folder without a store identity must not derive a shared id")
	}
}

// ensureProjectRow: a new folder gets the derived id; a folder with a
// _meta.json keeps its id (even one an older version minted at random) and
// gains its row; stray duplicate rows are listed, never chosen.
func TestEnsureProjectRowResolvesThroughMeta(t *testing.T) {
	key := storeFolderKey{Store: "github.com/northwind/lore", Branch: "main", Folder: "org"}
	want, _ := storeFolderProjectID(key)

	fresh := map[string][]byte{}
	got, err := ensureProjectRow(fresh, key)
	if err != nil || got != want {
		t.Fatalf("new folder: got %s, %v; want %s", got, err, want)
	}
	if _, ok := fresh[projectRowPath(want)]; !ok || len(strayProjectRows(fresh, got)) != 0 {
		t.Fatalf("new folder files: %v", fresh)
	}
	// Idempotent: a second pass over the same files changes nothing.
	n := len(fresh)
	if again, err := ensureProjectRow(fresh, key); err != nil || again != want || len(fresh) != n {
		t.Fatalf("second pass: %s, %v, %d files", again, err, len(fresh))
	}

	const oldID, strayID = "prj_0190aaaa0000700080000000000000aa", "prj_0190aaaa0000700080000000000000ab"
	meta, err := (stores.StoreMeta{ProjectID: oldID}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	old := map[string][]byte{"_meta.json": meta, projectRowPath(strayID): []byte("{}"), "tasks/tsk_1.json": []byte("{}")}
	got, err = ensureProjectRow(old, key)
	if err != nil || got != oldID {
		t.Fatalf("old folder: got %s, %v; want _meta.json's %s", got, err, oldID)
	}
	if _, ok := old[projectRowPath(oldID)]; !ok {
		t.Fatal("the _meta.json project gains its missing row")
	}
	if s := strayProjectRows(old, got); len(s) != 1 || s[0] != strayID {
		t.Fatalf("stray rows: %v", s)
	}
}
