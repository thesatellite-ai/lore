package wire

// coord_store_hold.go — hold claim / hold release on an org store's
// signals ref (devco:D-672). A hold had only the code repo's place: from
// the home folder `hold claim --name staging` refused with the git hint,
// although a resource an org shares (a staging slot, a test suite) belongs
// on the org store's signals ref, the place store task claims already go
// from home (D-635).
//
//	HoldClaim / HoldRelease ─► holdWorld
//	   --store <org>[/<folder>] (anywhere), or --setup from outside every
//	   code repo, or a store clone: storeQueueScopeOf, as task next
//	   one folder of one store ─► storeWorldOn, the world's folder = it
//	   else (a code repo, nothing named) ─► openWorld + forHolds, unchanged
//
// The hold lands at <folder>/resources/<name> on the store's signals ref,
// the same path a linked code repo of that folder holds it at, so a
// teammate there sees it held. The D-580 rule (no hold without --push
// unless live.autopush is chosen), the org identity signing the signals
// commit and the release taking the hold off the store's remote are the
// store world's (useStore), as for a store task claim.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// holdVerb names the hold command in refusals and printed steps.
type holdVerb string

const (
	holdVerbClaim   holdVerb = "claim"
	holdVerbRelease holdVerb = "release"
)

// holdWorld is the coordination world a hold of name runs on: the org
// store folder store (or the --setup / store clone scope) names, else the
// code repo's (openWorld, forHolds). push is " --push" when the printed
// retry of a claim needs it ("" for a release).
func (h standaloneCoord) holdWorld(ctx context.Context, verb holdVerb, name, store, push string) (*coordWorld, error) {
	a := h.a
	if store == "" && inCodeRepo(ctx, a.opts.RepoDir) {
		w, err := h.openWorld(ctx)
		if err != nil {
			return nil, err
		}
		return w, w.forHolds(ctx)
	}
	sc, ok, err := h.storeQueueScopeOf(ctx, opscoord.NextIn{Store: store})
	if err != nil {
		return nil, err
	}
	if !ok {
		// the home folder or a plain folder with no store named: a hold
		// needs a place; name the org store one
		return nil, errs.New(errs.Unsupported, "a hold lives on a signals ref: a code repo's, or an org store's; here is neither").
			WithHint("`lore hold " + string(verb) + " --name " + name + " --store <org>/<folder>" + push + "` holds it on that org store folder (`lore store folder list --store <org>` lists them); or run it inside the code repo")
	}
	f, err := oneHoldFolder(sc, verb, name, push)
	if err != nil {
		return nil, err
	}
	cfg, _, cerr := orgcfg.LoadConfig(a.opts.Home)
	if cerr != nil {
		return nil, errs.Wrap(errs.Internal, "read config.yaml", cerr)
	}
	org := cfg.FindOrg(f.Org)
	if org == nil {
		return nil, errs.New(errs.NotFound, "no org entry of config.yaml owns the store of "+f.Label).
			WithHint("`lore org list` lists the org entries")
	}
	st := a.orgFolderStore(ctx, f)
	w, err := h.storeWorldOn(ctx, cfg, org, st, a.companyFolderOf(ctx, st)...)
	if err != nil {
		return nil, err
	}
	folder := f.Folder
	if folder == "" || folder == rowfs.FolderRoot {
		folder = signalsRootFolder
	}
	w.folder, w.spot.folder = folder, folder
	return w, nil
}

// oneHoldFolder is the one org store folder of scope sc a hold goes to:
// a hold names one place, so a scope of several folders is refused with
// the command that lists them and the hold command naming one (the
// folders themselves vary by store, so the refusal reads the same in
// every store clone of the same shape).
func oneHoldFolder(sc storeQueueScope, verb holdVerb, name, push string) (orgFolder, error) {
	if len(sc.folders) == 1 {
		return sc.folders[0], nil
	}
	org := sc.folders[0].Org
	return orgFolder{}, errs.New(errs.InvalidInput, "a hold names one org store folder, and "+holdScopeName(sc)+" holds several folders of "+org+": name one").
		WithHint("`lore store folder list --store " + org + "` lists them; then `lore hold " + string(verb) + " --name " + name + " --store " + org + targetSep + "<folder>" + push + "`")
}

// holdScopeName names scope sc in a refusal.
func holdScopeName(sc storeQueueScope) string {
	if sc.flag != "" {
		return strings.TrimSpace(sc.flag)
	}
	return "this store clone"
}

// holdStoreRun reports a hold claim / release that runs on an org store
// (devco:D-672): it names --store, or it runs outside every code repo. The
// run opens nothing first (holdWorld opens the store itself); the repo open
// would refuse with E_NOT_A_STORE.
func (a *App) holdStoreRun(ctx context.Context, e op.Entry, d op.Decoder) bool {
	if n := e.Spec().Name; n != opscoord.OpHoldClaim && n != opscoord.OpHoldRelease {
		return false
	}
	if !inCodeRepo(ctx, a.opts.RepoDir) {
		return true
	}
	in, err := e.Decode(d)
	if err != nil {
		return false
	}
	s, ok := in.String("store")
	return ok && s != ""
}
