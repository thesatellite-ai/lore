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
//	  folder                 ──▶ --to <org>/<folder>       (company when the
//	                             folder is of its org's own store: that
//	                             org's admins, devco D-681)
//	  repo, or the default   ──▶ the repo: no --to in a repo with .lore/,
//	                             --to <org>/<folder> in a linked repo,
//	                             --to <store>/<folder> of the link when
//	                             that folder is not attached (D-675)

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
// store folder; when that folder is not attached here, the link's own
// --to (Options.RepoLinkTarget, devco D-675). ok is false when that place
// cannot be named here (a linked repo whose store folder is not attached
// and whose link names no store of config.yaml): the caller must not
// print a command then, since no --to it could print would work (devco
// D-658: it printed the literal `--to <store>` and `--to <store>/<folder>`).
func (a *App) PolicyPlace(ctx context.Context, key policy.Key) (to string, company bool, ok bool) {
	to, company, _, ok = a.PolicyPlaceJudged(ctx, key, profilestore.NewJudge(a))
	return to, company, ok
}

// PolicyPlaceJudged is PolicyPlace judged by j (one per render, so each
// org's roster opens once), plus the admins who may write there when the
// place is company ground. The place is judged as the write's role check
// judges it (profilestore.RequireAdminAt): by the roster of its own
// store's org, and the repo ("" --to) inside a store clone as the folder
// it is (devco D-681: in the ops store clone an ops folder's budget was
// judged by the company roster and printed to a member as their command).
func (a *App) PolicyPlaceJudged(ctx context.Context, key policy.Key, j *profilestore.Judge) (to string, company bool, admins profilestore.Admins, ok bool) {
	to, ok = a.policyPlace(ctx, key)
	if !ok {
		return to, false, profilestore.Admins{}, false
	}
	place := store.Source(to)
	if to == "" {
		place = store.SourceRepo
	}
	company, admins = j.At(ctx, place)
	return to, company, admins, true
}

// policyPlace is PolicyPlace's --to and ok.
func (a *App) policyPlace(ctx context.Context, key policy.Key) (to string, ok bool) {
	st, err := a.state(ctx)
	if err != nil {
		return "", true
	}
	level := policy.LevelRepo
	if set, perr := st.eng.Policy(ctx, store.SourceRepo); perr == nil {
		if eff, has := set.Get(key); has && !eff.IsDefault && eff.Level != "" {
			level = eff.Level
			if name, found := policyRowStore(ctx, st.eng, eff.Source); found && name != store.SourceRepo {
				return a.StoreLabel(name), true
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
			return a.StoreLabel(src), true
		}
	case policy.LevelStoreRoot:
		if src, has := onlyStoreAt(st.eng, policy.LevelStoreRoot); has {
			return a.StoreLabel(src), true
		}
		return "", false
	case policy.LevelFolder:
		if src, has := aliases[attach.ReservedTeam]; has {
			return a.StoreLabel(src), true
		}
	}
	if a.LinkedByRemote(ctx) == "" {
		return "", true
	}
	if src, has := aliases[attach.ReservedTeam]; has {
		return a.StoreLabel(src), true
	}
	if src, has := onlyStoreAt(st.eng, policy.LevelFolder); has {
		return a.StoreLabel(src), true
	}
	// devco D-675: the linked folder is not attached here, but the link
	// still names it, and a write's --to resolves "<store>/<folder>" from
	// config.yaml (Options.RepoLinkTarget, D-670): print that real --to
	// instead of no command at all
	if a.opts.RepoLinkTarget != nil {
		if t := a.opts.RepoLinkTarget(ctx); t != "" {
			return t, true
		}
	}
	return "", false
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
