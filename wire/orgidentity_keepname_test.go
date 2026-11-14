package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	opsidentity "github.com/thesatellite-ai/lore/ops/identity"
)

// TestSetOrgIdentityKeepsName (devco N-12): `identity set --org acme --key
// human:<new email>`, the documented email change, keeps the name and
// handle the org entry has; a name given replaces it.
func TestSetOrgIdentityKeepsName(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := "orgs:\n  - name: acme\n    identity:\n      key: human:ana@acme.example\n      name: Ana-María de la Peña\n      handle: ana\n    stores:\n      - name: notes\n        url: https://example.com/acme/notes\n"
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{opts: Options{Home: home}}
	got, _, err := a.SetOrgIdentity(context.Background(), "acme", opsidentity.Entry{Key: "human:ana.new@acme.example"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Ana-María de la Peña" || got.Handle != "ana" || got.Key != "human:ana.new@acme.example" {
		t.Fatalf("written = %+v, want the new key with the old name and handle", got)
	}
	mc, err := readMachineConfig(home)
	if err != nil || mc.Orgs[0].Identity.Name != "Ana-María de la Peña" || mc.Orgs[0].Identity.Handle != "ana" {
		t.Fatalf("config identity = %+v, %v", mc.Orgs[0].Identity, err)
	}
	if got, _, err = a.SetOrgIdentity(context.Background(), "acme", opsidentity.Entry{Key: "human:ana.new@acme.example", Name: "Ana Peña"}); err != nil || got.Name != "Ana Peña" {
		t.Fatalf("a given name: %+v, %v", got, err)
	}
}

// TestOrgSignerNeverTheOrgName (devco N-12): an org identity with no name
// signs its store clones with the email's local part, never the org's
// name (a clone's git user.name became "acme").
func TestOrgSignerNeverTheOrgName(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := "orgs:\n  - name: acme\n    identity:\n      key: human:ana@acme.example\n    stores:\n      - name: notes\n        url: https://example.com/acme/notes\n"
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	signers, err := NewOrgResolution(home, t.TempDir(), nil).OrgSigners(context.Background())
	if err != nil || len(signers) != 1 || signers[0].Name != "ana" || signers[0].Email != "ana@acme.example" {
		t.Fatalf("OrgSigners = %+v, %v; want ana <ana@acme.example>", signers, err)
	}
}
