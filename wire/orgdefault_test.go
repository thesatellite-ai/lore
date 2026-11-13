package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestDefaultOrgRefusalNamesTheFlag: an org verb run without --org on a
// machine with two org entries and no setup naming one is refused with a
// hint in the named form (`lore org refresh --org acme`), never a
// positional org (named args everywhere).
func TestDefaultOrgRefusalNamesTheFlag(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p := orgcfg.ConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("orgs:\n  - name: acme\n  - name: beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := (&standaloneOrg{home: home}).DefaultOrg(context.Background(), "org refresh")
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.InvalidInput {
		t.Fatalf("got %v, want E_INVALID_INPUT", err)
	}
	if !strings.Contains(e.Hint, "`lore org refresh --org acme`") {
		t.Fatalf("hint %q does not show the named form `lore org refresh --org acme`", e.Hint)
	}
}
