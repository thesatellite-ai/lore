package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// TestPersonalMoveFixNamesMigrateOnOldLayout (E-12): a personal store
// clone still on lore 0.1.17's layout moves its everywhere/ notes only in
// lore migrate run, so doctor names that; once the store has the new
// layout, the next lore command moves them.
func TestPersonalMoveFixNamesMigrateOnOldLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := func(version string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rowfs.MarkerFile), []byte("lore_store: "+version+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	marker("1")
	if got := personalMoveFix(dir); !strings.HasPrefix(got, "lore migrate run") {
		t.Fatalf("fix on the 0.1.17 layout = %q, want lore migrate run", got)
	}
	marker("4\nid: sto_01a1a59b891c70b789144cd3e711fc21\nvisibility: internal")
	if got := personalMoveFix(dir); !strings.HasPrefix(got, "run any lore command") {
		t.Fatalf("fix on the new layout = %q", got)
	}
}
