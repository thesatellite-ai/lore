package orgcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestLoadConfigBrokenLoreHome: when ~/.lore is a file, reading the org
// config is E_BAD_PATH naming config.yaml with a fix line, never
// E_INTERNAL "orgcfg: read" (the machine sweep's env_lore_home_file:
// whoami, org use, role list and contact lists all failed that way).
func TestLoadConfigBrokenLoreHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".lore")
	if err := os.WriteFile(home, []byte("not a folder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadConfig(home)
	var e *errs.Error
	if !errs.Has(err, errs.BadPath) || !asErr(err, &e) || e.Hint == "" || !strings.Contains(e.Message, "config.yaml") {
		t.Fatalf("broken ~/.lore: %v", err)
	}
}

func asErr(err error, e **errs.Error) bool {
	x, ok := err.(*errs.Error)
	*e = x
	return ok
}
