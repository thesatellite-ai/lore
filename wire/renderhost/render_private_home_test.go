package renderhost

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// TestPrivateImportNamesTheHomeInUse (devco D-535): the import lines of a
// private render name <lore home>/render/ (~-relative under the person's
// home, else absolute), never ~/.lore/render/ for another home; lines an
// older lore wrote with ~/.lore/render/ are replaced, not kept beside.
func TestPrivateImportNamesTheHomeInUse(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	if got := privateImportDirFor(filepath.Join(user, ".lore")); got != "~/.lore/render/" {
		t.Fatalf("default home: %q", got)
	}
	if got := privateImportDirFor(filepath.Join(user, ".lore-dev")); got != "~/.lore-dev/render/" {
		t.Fatalf("a preview home: %q", got)
	}
	other := filepath.Join(t.TempDir(), "elsewhere")
	if got := privateImportDirFor(other); got != filepath.ToSlash(other)+"/render/" {
		t.Fatalf("a home outside the home folder: %q", got)
	}
	prof := toolprofile.Profile{Name: "claude_code", Import: toolprofile.ImportAt, PrivateFile: "CLAUDE.local.md"}
	old := "my own line\n\n@~/.lore/render/web-r1/team-s1.md\n"
	in := prof.PrivateContent(old, privateImportDir, nil)
	out := prof.PrivateContent(in, "~/.lore-dev/render/", []string{"~/.lore-dev/render/web-r1/team-s1.md"})
	if strings.Contains(out, "@~/.lore/render/") || !strings.Contains(out, "@~/.lore-dev/render/web-r1/team-s1.md") || !strings.Contains(out, "my own line") {
		t.Fatalf("the old import must be replaced by the home in use, the person's line kept:\n%s", out)
	}
}
