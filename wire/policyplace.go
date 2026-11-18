package wire

// policyplace.go — where a printed `lore policy set` must write to change
// a key's value here, and who may (devco D-636, D-637). The context
// budget warning `lore sync` prints named no --to, so in a repo a store
// links by its remote (no .lore/ of its own) it fell to --to repo and was
// refused; and it told every member to demote a company must rule.
//
//	effective value's level ──▶ the place a raise must go
//	  org folder             ──▶ --to <org>/org            (company: admins)
//	  store root             ──▶ --to <store>              (company: admins)
//	  folder                 ──▶ --to <org>/<folder>       (members too)
//	  repo, or the default   ──▶ the repo: no --to in a repo with .lore/,
//	                             --to <org>/<folder> in a linked repo

import (
	"context"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/store"
)

// PolicyPlace is the --to a policy write needs to change key's effective
// value here ("" = the repo default), and whether that place is a company
// place (only an admin of the org writes a policy there). The policy is
// the strictest level's, so a raise goes to the level that sets it now;
// with no row, to the repo, which in a linked repo is its store folder.
func (a *App) PolicyPlace(ctx context.Context, key policy.Key) (string, bool) {
	st, err := a.state(ctx)
	if err != nil {
		return "", false
	}
	level := policy.LevelRepo
	if set, perr := st.eng.Policy(ctx, store.SourceRepo); perr == nil {
		if eff, ok := set.Get(key); ok && !eff.IsDefault && eff.Level != "" {
			level = eff.Level
		}
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		top = ""
	}
	aliases := orgAliases(ctx, a.opts, top)
	switch level {
	case policy.LevelOrg:
		if src, ok := aliases[attach.ReservedCompany]; ok {
			return a.StoreLabel(src), true
		}
	case policy.LevelStoreRoot:
		return "<store>", true
	case policy.LevelFolder:
		if src, ok := aliases[attach.ReservedTeam]; ok {
			return a.StoreLabel(src), false
		}
	}
	if a.LinkedByRemote(ctx) == "" {
		return "", false
	}
	if src, ok := aliases[attach.ReservedTeam]; ok {
		return a.StoreLabel(src), false
	}
	return "<store>/<folder>", false
}

// OrgAdmins is who the roster of this folder's org names as its admins
// and whether the caller is one, for a hint that may only send an admin's
// command to an admin (devco D-637). ok is false when no org roster is
// readable here (no org: nothing is admin-only).
func (a *App) OrgAdmins(ctx context.Context) (profilestore.Admins, bool) {
	r, done, err := a.Roster(ctx, false)
	defer done()
	if err != nil || r == nil || r.Source() == store.SourceRepo {
		return profilestore.Admins{}, false
	}
	ad, err := profilestore.ReadAdmins(ctx, r)
	if err != nil {
		return profilestore.Admins{}, false
	}
	return ad, true
}
