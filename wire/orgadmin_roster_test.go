package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestOrgAdminOpsFollowTheRosterH109: who may run an org admin op is the
// admin role on the org's roster, not an admin setup on this machine
// (H-109). The org's creator, who ran `org create` with no setup, changes
// lore-org.yaml; a member whose machine holds an admin setup of the org
// is refused by org config set, org store audience, org store remove and
// store folder remove, and the refusal names who they are and what the
// roster says; the creator then removes the folder. Break-verified: with
// the old machine-setup check the creator is refused and the member passes.
func TestOrgAdminOpsFollowTheRosterH109(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	if hasOrgAdminSetup(mira, "agency") {
		t.Fatal("fixture: the creator must hold no admin setup")
	}
	if !orgRosterAdmin(ctx, mira, "agency") {
		t.Fatal("the org's creator is not its admin on the roster")
	}
	set := opsorgcmd.ConfigSetIn{Org: "agency", Key: opsorgcmd.ConfigKeyOwn, Add: []string{"github.com/agency-labs/*"}}
	rep, err := h.OrgConfigSet(ctx, set, true)
	if err != nil {
		t.Fatalf("org config set by the creator with no setup: %v", err)
	}
	if text := strings.Join(rep.Lines, "\n"); !strings.Contains(text, "✓ own in") {
		t.Fatalf("org config set by the creator:\n%s", text)
	}
	folder := `{"store":"agency","folder":"client_x"}`
	ma := w.app(t, w.repo, mira)
	if _, err := run(t, ma, opsstores.OpFolderAdd, folder, false); err != nil {
		t.Fatalf("folder add by the creator: %v", err)
	}
	w.push(t, mira, orgTestStoreURL)

	// bob joins as a member; his machine claims an admin setup of the org
	bob := w.home(t, "bob")
	out := filepath.Join(w.root, "outside")
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	hb := orgHostAt(t, out, bob)
	if _, err := hb.Import(ctx, opsorgcmd.ImportIn{URL: orgTestImport, Key: "human:bob@agency.example", Name: "Bob Roe", Handle: "bob"}, true); err != nil {
		t.Fatalf("import: %v", err)
	}
	cfg := readConfig(t, bob)
	if strings.Contains(cfg, "setups:") {
		t.Fatalf("fixture: bob's config already lists setups:\n%s", cfg)
	}
	cfg += "setups:\n  - name: agency-admin\n    kind: admin\n    org: agency\n"
	if err := os.WriteFile(configPath(bob), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasOrgAdminSetup(bob, "agency") {
		t.Fatal("fixture: bob's machine must hold an admin setup of agency")
	}
	if orgRosterAdmin(ctx, bob, "agency") {
		t.Fatal("a member with an admin setup counts as the org's admin")
	}
	refused := func(what string, err error) {
		t.Helper()
		wantCode(t, err, errs.Refused, what)
		if msg := err.Error(); !strings.Contains(msg, "human:bob@agency.example") || !strings.Contains(msg, "no admin role on the roster of agency") || strings.Contains(msg, "admin setup") {
			t.Fatalf("%s: the refusal must name the person and the roster, not a setup: %s", what, msg)
		}
	}
	_, err = hb.OrgConfigSet(ctx, set, true)
	refused("org config set by a member with an admin setup", err)
	_, err = hb.SetStoreAudience(ctx, opsorgcmd.StoreAudienceIn{Org: "agency", URL: orgTestStoreURL, Audience: "admins"}, true)
	refused("org store audience by a member with an admin setup", err)
	_, err = hb.RemoveStore(ctx, opsorgcmd.StoreRemoveIn{Org: "agency", URL: "https://github.com/agency/empty"}, true)
	refused("org store remove by a member with an admin setup", err)
	_, err = run(t, w.app(t, out, bob), opsstores.OpFolderRemove, folder, true)
	refused("store folder remove by a member with an admin setup", err)

	w.home(t, "mira")
	if _, err := run(t, w.app(t, w.repo, mira), opsstores.OpFolderRemove, folder, true); err != nil {
		t.Fatalf("store folder remove by the creator with no setup: %v", err)
	}
}

// app is lore's App for the person whose lore home is home, run in dir.
func (w *orgWorld) app(t *testing.T, dir, home string) *App {
	t.Helper()
	a, err := New(StandaloneOptions(dir, home, dir, "test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}
