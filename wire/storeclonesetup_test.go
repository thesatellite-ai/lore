package wire

import (
	"context"
	"testing"

	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// TestResolveSetupInStoreClone (devco D-693): in a clone of an org's store
// (its top and a folder) no folder rule and no repo org rule match, yet
// the clone is that org's: step 5 chooses the org's one setup, and its
// org is the hint the org resolution uses (so the org's identity, not the
// OS user, writes there and the role check sees the admin). A clone of a
// store no org entry holds still resolves no setup.
func TestResolveSetupInStoreClone(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, `orgs:
  - name: acme
    identity: {key: "human:ada@acme.example"}
    stores:
      - url: git@github.com:acme-co/lore-team.git
  - name: acmeops
    identity: {key: "human:ada@acmeops.example"}
    stores:
      - url: git@github.com:acmeops-co/lore-team.git
setups:
  - name: acme-admin
    kind: admin
    org: acme
    folders: [~/code/**]
  - name: acmeops-admin
    kind: admin
    org: acmeops
    folders: [~/ops/**]
`)
	top, folder := fakeStoreClone(t)
	gitRunT(t, top, "remote", "add", "origin", "git@github.com:acme-co/lore-team.git")
	noOrg := func(context.Context) (string, error) { return "", nil }
	for _, dir := range []string{top, folder} {
		q := SetupQuery{Home: home, Dir: dir, UserHome: "/home/ada", Getenv: func(string) string { return "" }, OrgOf: noOrg}
		res, err := ResolveSetup(context.Background(), q)
		if err != nil || res.Active.Name != "acme-admin" || res.Active.Why != opssetup.WhyOrg {
			t.Fatalf("%s: got %+v %v, want acme-admin by the store's org", dir, res.Active, err)
		}
		if h := setupHint(res); h.Org != "acme" {
			t.Fatalf("%s: a setup chosen by the store clone's org is the org hint, got %+v", dir, h)
		}
	}
	other, _ := fakeStoreClone(t)
	gitRunT(t, other, "remote", "add", "origin", "git@github.com:someone/else.git")
	q := SetupQuery{Home: home, Dir: other, UserHome: "/home/ada", Getenv: func(string) string { return "" }, OrgOf: noOrg}
	res, err := ResolveSetup(context.Background(), q)
	if err != nil || res.Active.Name != "" {
		t.Fatalf("a store of no org entry: got %+v %v, want none", res.Active, err)
	}
}
