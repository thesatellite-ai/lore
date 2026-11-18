package wire

// policyplace.go — where a printed `lore policy set` must write to change
// a key's value here, and who may (devco D-636, D-637). The context
// budget warning `lore sync` prints named no --to, so in a repo a store
// links by its remote (no .lore/ of its own) it fell to --to repo and was
// refused; and it told every member to demote a company must rule.
//
//	effective value's level ──▶ the place a raise must go
//	  org folder             ──▶ --to <org>/org            (company: admins)
//	  store root             ──▶ --to <the store's name>   (company: admins)
//	  folder                 ──▶ --to <org>/<folder>       (members too)
//	  repo, or the default   ──▶ the repo: no --to in a repo with .lore/,
//	                             --to <org>/<folder> in a linked repo

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// PolicyPlace is the --to a policy write needs to change key's effective
// value here ("" = the repo default), and whether that place is a company
// place (only an admin of the org writes a policy there). The policy is
// the strictest level's, so a raise goes to the store that holds the row
// setting it now; with no row, to the repo, which in a linked repo is its
// store folder. ok is false when that place cannot be named here (a
// linked repo whose store folder is not attached): the caller must not
// print a command then, since no --to it could print would work (devco
// D-658: it printed the literal `--to <store>` and `--to <store>/<folder>`).
func (a *App) PolicyPlace(ctx context.Context, key policy.Key) (to string, company bool, ok bool) {
	st, err := a.state(ctx)
	if err != nil {
		return "", false, true
	}
	level := policy.LevelRepo
	if set, perr := st.eng.Policy(ctx, store.SourceRepo); perr == nil {
		if eff, has := set.Get(key); has && !eff.IsDefault && eff.Level != "" {
			level = eff.Level
			if name, found := policyRowStore(ctx, st.eng, eff.Source); found && name != store.SourceRepo {
				return a.StoreLabel(name), profilestore.CompanyLevel(st.eng.Level(name)), true
			}
		}
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		top = ""
	}
	aliases := orgAliases(ctx, a.opts, top)
	switch level {
	case policy.LevelOrg:
		if src, has := aliases[attach.ReservedCompany]; has {
			return a.StoreLabel(src), true, true
		}
	case policy.LevelStoreRoot:
		if src, has := onlyStoreAt(st.eng, policy.LevelStoreRoot); has {
			return a.StoreLabel(src), true, true
		}
		return "", true, false
	case policy.LevelFolder:
		if src, has := aliases[attach.ReservedTeam]; has {
			return a.StoreLabel(src), false, true
		}
	}
	if a.LinkedByRemote(ctx) == "" {
		return "", false, true
	}
	if src, has := aliases[attach.ReservedTeam]; has {
		return a.StoreLabel(src), false, true
	}
	if src, has := onlyStoreAt(st.eng, policy.LevelFolder); has {
		return a.StoreLabel(src), false, true
	}
	return "", false, false
}

// policyRowStore is the attached store whose policies table holds the
// row id (the effective value's source), so a printed raise names the
// place that sets the value, never a placeholder.
func policyRowStore(ctx context.Context, eng *engine.Engine, id ids.AnyID) (store.Source, bool) {
	if id.IsZero() {
		return "", false
	}
	for _, name := range eng.Stores() {
		rows, err := eng.List(ctx, name, schema.TablePolicies)
		if err != nil {
			continue
		}
		for _, r := range rows {
			if r.ID == id {
				return name, true
			}
		}
	}
	return "", false
}

// onlyStoreAt is the one attached store at settings level lv; with none,
// or several (the place would be a guess), false.
func onlyStoreAt(eng *engine.Engine, lv policy.Level) (store.Source, bool) {
	var got []store.Source
	for _, name := range eng.Stores() {
		if eng.Level(name) == lv {
			got = append(got, name)
		}
	}
	if len(got) != 1 {
		return "", false
	}
	return got[0], true
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
