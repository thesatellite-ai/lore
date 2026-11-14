package host

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/row"
)

// TestWrongRowID (H-6): a row file whose id is not its file name is
// refused by the pre-commit check (every read on every clone leaves it
// out), and a duplicate id names the other file. Before, both commits
// passed: only invalid JSON and conflict markers were refused.
func TestWrongRowID(t *testing.T) {
	root := t.TempDir()
	doc := memRow(t, 1, mdActorA, "episodic", "x")
	r, err := row.Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	own := loreDataPathspec + "/" + rowfs.RowPath(r.Table, r.ID)
	if why := wrongRowID(root, own, doc); why != "" {
		t.Fatalf("its own file: %q, want fine", why)
	}
	other := loreDataPathspec + "/memories/mem_01a1d7ffcfea7743a60a9770051affff.json"
	why := wrongRowID(root, other, doc)
	if !strings.Contains(why, "but its file name says mem_01a1d7ffcfea7743a60a9770051affff") || strings.Contains(why, "duplicate") {
		t.Fatalf("wrong id: %q", why)
	}
	full := filepath.Join(root, filepath.FromSlash(own))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	if why := wrongRowID(root, other, doc); !strings.Contains(why, own+" holds that id too (a duplicate id)") {
		t.Fatalf("duplicate id: %q", why)
	}
	if why := wrongRowID(root, loreDataPathspec+"/"+rowfs.MetaFileName, []byte(`{"_v":2}`)); why != "" {
		t.Fatalf("_meta.json is no row: %q", why)
	}
}

// TestUnreadableWhy (H-2): an unreadable changed file is named with the
// cause and the chmod, not E_INTERNAL "pre-commit: read <path>".
func TestUnreadableWhy(t *testing.T) {
	err := &fs.PathError{Op: "open", Path: "/x/a.json", Err: fs.ErrPermission}
	if got := unreadableWhy("/x/a.json", err); got != "cannot be read: permission denied; make it readable again: chmod u+rw /x/a.json" {
		t.Fatalf("got %q", got)
	}
}
