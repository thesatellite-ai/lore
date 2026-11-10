package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestSetUnsetIdentity (CFG9): set writes identity: into config.yaml,
// keeps the other keys and moves a v0.1.17 identity.yaml aside; unset
// clears it; unset --org of an unknown org is E_NOT_FOUND.
func TestSetUnsetIdentity(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := filepath.Join(home, ConfigFile)
	if err := os.WriteFile(cfg, []byte("live:\n  autopush: false\norgs:\n  - name: agency\n    identity:\n      key: human:a@agency.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, identity.LegacyFile)
	if err := os.WriteFile(legacy, []byte("stable_key: human:old@x.io\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, migrated, err := SetIdentity(home, identity.Entry{Key: "human:alice@example.com", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if migrated != legacy+LegacyMigratedSuffix {
		t.Fatalf("migrated = %q", migrated)
	}
	if _, serr := os.Stat(legacy); !os.IsNotExist(serr) {
		t.Fatal("identity.yaml is still in place")
	}
	mc, err := readMachineConfig(home)
	if err != nil || mc.Identity.Key != "human:alice@example.com" || mc.Identity.Name != "Alice" {
		t.Fatalf("identity = %+v, %v", mc.Identity, err)
	}
	b, _ := os.ReadFile(filepath.Clean(cfg))
	if !strings.Contains(string(b), "autopush: false") {
		t.Fatalf("other keys lost:\n%s", b)
	}
	if found, _, uerr := UnsetIdentity(home, "agency"); uerr != nil || !found {
		t.Fatalf("unset --org agency: %v %v", found, uerr)
	}
	if found, _, uerr := UnsetIdentity(home, ""); uerr != nil || !found {
		t.Fatalf("unset: %v %v", found, uerr)
	}
	if found, _, uerr := UnsetIdentity(home, ""); uerr != nil || found {
		t.Fatalf("second unset: %v %v", found, uerr)
	}
	mc, _ = readMachineConfig(home)
	if mc.Identity.Key != "" || len(mc.Orgs) != 1 || mc.Orgs[0].Identity.Key != "" {
		t.Fatalf("after unset: %+v", mc)
	}
	if _, _, uerr := UnsetIdentity(home, "nope"); !errs.Has(uerr, errs.NotFound) {
		t.Fatalf("unknown org: %v", uerr)
	}
}
