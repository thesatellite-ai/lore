package wire

import (
	"strings"
	"testing"
)

// TestFixNoSetupNoteNamesDirs (devco:K-17): fix --all's no-setup note
// names extra folders with --dirs, as `lore fix --help` and the docs do,
// never positionally.
func TestFixNoSetupNoteNamesDirs(t *testing.T) {
	t.Parallel()
	if !strings.Contains(fixNoSetupFolders, "lore fix --all --dirs ~/code --dirs ~/work") || strings.Contains(fixNoSetupFolders, "--all ~/") {
		t.Fatalf("note: %q", fixNoSetupFolders)
	}
}
