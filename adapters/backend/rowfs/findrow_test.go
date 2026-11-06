package rowfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// TestFindRow (later 106): FindRow names the data folder that holds a
// row's file — the store root (FolderRoot), a project folder, org, a
// nested folder, a folder name with a space or unicode — and never one
// under a dot dir (.git, .cache, a .lore that is not a folder's own); a
// row in no folder is not found; a missing table or id is
// E_INVALID_INPUT; an unreadable folder is an error, never "not found".
func TestFindRow(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	put := func(folder string, id ids.AnyID) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(folder), LoreDir, DataDir, filepath.FromSlash(RowPath(memories, id)))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	newID := func() ids.AnyID {
		t.Helper()
		id, err := ids.New[ids.Memory]()
		if err != nil {
			t.Fatal(err)
		}
		return id.Any()
	}

	cases := []struct {
		folder string // where the row file is put; "" = nowhere
		want   string
		found  bool
	}{
		{".", FolderRoot, true},
		{"client1", "client1", true},
		{"org", "org", true},
		{"clients/acme", "clients/acme", true},
		{"my folder ✓", "my folder ✓", true},
		{".git", "", false},
		{".cache/client1", "", false},
		{"client2/.lore/client3", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		id := newID()
		if c.folder != "" {
			put(c.folder, id)
		}
		got, found, err := r.FindRow(memories, id)
		if err != nil || found != c.found || got != c.want {
			t.Errorf("row in %q: %q found=%v err=%v, want %q found=%v", c.folder, got, found, err, c.want, c.found)
		}
		if _, found, err = r.FindRow("tasks", id); err != nil || found {
			t.Errorf("row in %q under another table: found=%v err=%v", c.folder, found, err)
		}
	}

	if _, _, err = r.FindRow(memories, ids.AnyID{}); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("zero id: %v, want E_INVALID_INPUT", err)
	}
	if _, _, err = r.FindRow(row.Table(""), newID()); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("no table: %v, want E_INVALID_INPUT", err)
	}

	locked := filepath.Join(dir, "locked")
	if err = os.MkdirAll(locked, 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) }) // let TempDir remove it
	if _, found, ferr := r.FindRow(memories, newID()); ferr == nil || found || !errs.Has(ferr, errs.Internal) {
		t.Errorf("an unreadable folder: found=%v err=%v, want an error", found, ferr)
	}
}
