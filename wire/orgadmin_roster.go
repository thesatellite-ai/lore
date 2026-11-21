package wire

// orgadmin_roster.go — who may run an org admin op (org config set, org
// store add/audience/remove, an org store's shared settings, its folders
// and history): the admin role on the org's roster, the same rule every
// reader applies when it replays a change of lore-org.yaml (orgcfg's
// authority replay judges the commit author against the roster). H-109:
// these ops asked whether this machine holds an admin setup of the org
// instead, so the org's creator, who ran `lore org create` with no setup,
// was refused, and a machine whose setup's person lost the admin role
// still passed (and its change was then held back by every reader).

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// orgRosterAdmin reports whether the person this machine is in org (the
// org entry's identity key, the author of every commit lore makes in the
// org's stores: storeSignature) holds the admin role on org's roster, as
// this machine's clone of the org folder's store records it. No org
// entry: not an admin. When the roster cannot be judged here (no identity
// key, no clone yet, or a store with no lore-org.yaml: an org entry
// written by hand), the machine's admin setup answers, as ops/team's
// demotedAdmin keeps the setup's answer for a roster it cannot read; the
// store's own checks still hold back what a non-admin pushes.
func orgRosterAdmin(ctx context.Context, home, org string) bool {
	if org == "" {
		return false
	}
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return false
	}
	o := cfg.FindOrg(org)
	if o == nil {
		return false
	}
	sc := orgFolderStoreOf(o)
	if sc == nil || strings.TrimSpace(o.Identity.Key) == "" {
		return hasOrgAdminSetup(home, org)
	}
	g := &orgcfg.GitLinks{Home: home, Config: cfg, Route: storeRouter(home, "")}
	sg, err := g.LocalSuggestion(ctx, sc.URL, sc.Branch)
	if err != nil {
		return hasOrgAdminSetup(home, org)
	}
	return sg.IsAdmin(ctx, o.Identity.Key)
}

// orgFolderStoreOf is the store of org entry o that holds its org folder
// (roster and lore-org.yaml): the marked one, else its first store; nil
// when the entry lists none.
func orgFolderStoreOf(o *orgconf.Org) *orgconf.Store {
	if sc := o.OrgFolderStore(); sc != nil {
		return sc
	}
	if len(o.Stores) == 0 {
		return nil
	}
	return &o.Stores[0]
}

// notOrgAdminWhy is the end of a refusal of an org admin op: who this
// machine is in org and that the roster does not make them its admin.
func notOrgAdminWhy(home, org string) string {
	var o *orgconf.Org
	if cfg, _, err := orgcfg.LoadConfig(home); err == nil && cfg != nil {
		o = cfg.FindOrg(org)
	}
	if o == nil {
		return "this machine has no org entry " + org + " (lore org import --url <store-url> adds it), so the roster of " + org + " names no admin role of yours"
	}
	key := strings.TrimSpace(o.Identity.Key)
	if key == "" {
		return "this machine's org entry " + org + " names no identity key, so no admin role on the roster is yours"
	}
	return key + " (this machine's identity in " + org + ") holds no admin role on the roster of " + org
}
