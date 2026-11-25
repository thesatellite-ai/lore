package row

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// TestCarryTablesTakeTheFolderSettings (devco D-992): a bulk move that
// carries a whole folder into another store of its org takes the folder's
// own bookkeeping (policies, tags, metadata keys) with the notes, all but
// its project row (the new folder has its own); a plain bulk move still
// leaves that bookkeeping where it is.
func TestCarryTablesTakeTheFolderSettings(t *testing.T) {
	plain, carry := bulkTables(false), bulkTables(true)
	if !slices.Equal(plain, movableTables()) {
		t.Fatalf("bulkTables(false) is not movableTables: %v", plain)
	}
	for _, tb := range []string{"policies", "tags", "meta_keys"} {
		if slices.ContainsFunc(plain, func(x row.Table) bool { return string(x) == tb }) {
			t.Errorf("a plain bulk move takes %s", tb)
		}
		if !slices.ContainsFunc(carry, func(x row.Table) bool { return string(x) == tb }) {
			t.Errorf("a folder carry leaves %s behind", tb)
		}
	}
	if slices.Contains(carry, schema.TableProjects) {
		t.Error("a folder carry moves the old folder's project row")
	}
	for _, tb := range plain {
		if !slices.Contains(carry, tb) {
			t.Errorf("a folder carry leaves %s behind, which a plain move takes", tb)
		}
	}
}

// TestFolderCarryText (D-992): the dry run says the folder moves, with its
// links, and that the old one goes; the run says it was removed and where
// what was left of it is.
func TestFolderCarryText(t *testing.T) {
	c := FolderCarry{Folder: "shop", From: "acme/shop", To: "clientshop/shop", Links: 1}
	dry := c.text(true)
	for _, want := range []string{"would move folder shop from acme/shop to clientshop/shop", "its 1 repo link(s)", "acme/shop would be removed"} {
		if !strings.Contains(dry, want) {
			t.Errorf("dry run text lacks %q: %q", want, dry)
		}
	}
	c.Removed, c.Batch, c.Links = true, "bat_x", 0
	done := c.text(false)
	if !strings.Contains(done, "✓ moved folder shop to clientshop/shop") || !strings.Contains(done, "acme/shop removed") ||
		!strings.Contains(done, "bat_x") || strings.Contains(done, "link") {
		t.Errorf("run text: %q", done)
	}
}
