package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/op"
	opscomment "github.com/thesatellite-ai/lore/ops/comment"
)

// storeOwnerHome is a machine whose company entry (acme) keeps the ops
// org's store as an admins store of its own, while the ops org (acmeops)
// holds its org folder there: the owner's shape of devco:D-696.
const storeOwnerHome = `orgs:
  - name: acme
    identity: {key: "human:ada@acme.example"}
    own: ["github.com/acme-co/*"]
    stores:
      - url: git@github.com:acme-co/lore-team.git
        org_folder: org
      - url: git@github.com:acme-co/lore-acme-ops.git
  - name: acmeops
    identity: {key: "human:ada@acme.example"}
    stores:
      - url: git@github.com:acme-co/lore-acme-ops.git
        org_folder: org
setups:
  - name: acme-admin
    kind: admin
    org: acme
    folders: [~/code/acme/**]
    personal_store: none
  - name: acme-ops
    kind: admin
    org: acmeops
    folders: [~/code/acmeops/**]
    personal_store: none
`

// TestStoreOwnerOneRuleD696: every "whose store is this" answer names the
// same org for a store two org entries list: the entry holding its org
// folder there. Before, the setup picked for a row found by id took the
// first entry listing the store (the company) while the row's place was
// named by the last (the ops org), so `task claim --id <ops task>` from
// the home folder ran under the company's setup and refused "no store
// named acmeops … the stores --to takes here".
func TestStoreOwnerOneRuleD696(t *testing.T) {
	t.Parallel()
	const ops = "git@github.com:acme-co/lore-acme-ops.git"
	home := writeSetupsConfig(t, storeOwnerHome)
	a := openWith(t, t.TempDir(), Options{Home: home})
	k, ok := storeKey(ops)
	if !ok {
		t.Fatalf("storeKey(%s) failed", ops)
	}
	if got := a.storeOrgs()[k]; got != "acmeops" {
		t.Errorf("storeOrgs names the ops store %q, want acmeops (the entry holding its org folder)", got)
	}
	if got := orgOfURL(home, ops); got != "acmeops" {
		t.Errorf("orgOfURL(ops store) = %q, want acmeops", got)
	}
	if o := cloneOrgEntry(home, ops); o == nil || o.Name != "acmeops" {
		t.Errorf("cloneOrgEntry(ops store) = %+v, want acmeops", o)
	}
	if got := a.setupOfOrg(a.storeOrgs()[k]); got != "acme-ops" {
		t.Errorf("the setup of the ops store's org = %q, want acme-ops", got)
	}

	// the rule itself, in either order of the entries
	everyStore := func(orgconf.Store) bool { return true }
	company := orgconf.Org{Name: "acme", Stores: []orgconf.Store{{URL: ops}}}
	opsOrg := orgconf.Org{Name: "acmeops", Stores: []orgconf.Store{{URL: ops, OrgFolder: "org"}}}
	for _, orgs := range [][]orgconf.Org{{company, opsOrg}, {opsOrg, company}} {
		if o := storeOwnerOrg(orgs, everyStore); o == nil || o.Name != "acmeops" {
			t.Errorf("storeOwnerOrg(%s, %s) = %+v, want acmeops", orgs[0].Name, orgs[1].Name, o)
		}
	}
	plain := orgconf.Org{Name: "beta", Stores: []orgconf.Store{{URL: ops}}}
	if o := storeOwnerOrg([]orgconf.Org{company, plain}, everyStore); o == nil || o.Name != "beta" {
		t.Errorf("no entry holds an org folder there: want the last entry (beta), got %+v", o)
	}
	if o := storeOwnerOrg([]orgconf.Org{company}, func(orgconf.Store) bool { return false }); o != nil {
		t.Errorf("no entry lists the store: want nil, got %+v", o)
	}
}

// TestRowIDOfOnIDD696: a comment put on a row names that row by --on-id,
// and an id command from the home folder finds the row's store by it as
// --id does (devco:D-696: `comment add --on-id <task>` from home refused
// "not inside a git work tree"); --id wins when an input names both.
func TestRowIDOfOnIDD696(t *testing.T) {
	t.Parallel()
	a := openWith(t, t.TempDir(), Options{})
	e, ok := a.Registry().Lookup(opscomment.OpAdd)
	if !ok {
		t.Fatalf("no op %s", opscomment.OpAdd)
	}
	in, err := e.Decode(op.JSONDecoder{Body: []byte(`{"on_id":"tsk_01a1f34a753d7a698a7b26fbe67cf27d","body":"from home"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDOf(in); got != "tsk_01a1f34a753d7a698a7b26fbe67cf27d" {
		t.Errorf("rowIDOf(comment add --on-id) = %q, want the task id", got)
	}
}
