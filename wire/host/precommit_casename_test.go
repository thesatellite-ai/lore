package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/row"
)

// TestWrongRowIDCaseRename (devco Q-12): a case-only rename of a row file
// (mem_… → MEM_…, what a case-insensitive checkout or a Windows tool
// produces) was committed without a word, since a name that parses as no
// id at all skipped the id check; the row then vanished from every read
// of everyone who pulled. The pre-commit check now refuses it and names
// the git mv that puts the file back under its id.
func TestWrongRowIDCaseRename(t *testing.T) {
	root := t.TempDir()
	doc := memRow(t, 1, mdActorA, "episodic", "x")
	r, err := row.Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	own := loreDataPathspec + "/" + rowfs.RowPath(r.Table, r.ID)
	upper := loreDataPathspec + "/memories/" + strings.ToUpper(r.ID.String()) + ".json"
	why := wrongRowID(root, upper, doc)
	if !strings.Contains(why, "its file name is not that id") || !strings.Contains(why, "git mv "+upper+" "+own) {
		t.Fatalf("case-only rename: %q, want a refusal naming git mv %s %s", why, upper, own)
	}
	full := filepath.Join(root, filepath.FromSlash(own))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	if why := wrongRowID(root, upper, doc); !strings.Contains(why, own+" holds that id too (a duplicate id)") {
		t.Fatalf("both files present: %q, want the duplicate named", why)
	}
	if why := wrongRowID(root, loreDataPathspec+"/memories/notes.json", []byte(`{"a":1}`)); why != "" {
		t.Fatalf("a JSON file that is no row is not this check's: %q", why)
	}
}
