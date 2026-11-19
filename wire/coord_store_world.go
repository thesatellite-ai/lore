package wire

// coord_store_world.go — coordination of a store task with no code repo
// (devco:D-635). A store task's claim lives on its store's signals ref
// (refs/lore/signals/<branch> of the store clone), so it needs no code
// repo checkout: `task claim --id <store task>`, release and the other
// verbs that name one task run from the home folder or a plain folder
// and from inside the store clone on a world built from the clone alone.
//
//	openWorldFor(id) ─► openStoreWorld: here is no code repo
//	   find the folder holding id (rowFolderIn, the D-553 id scan)
//	   org entry of the store holding it (orgOfRow)
//	   App opened store-only on that folder (as runStoreOnly does)
//	   coordWorld{repo: the store clone} ─► useStore: the store's ref
//
// The D-580 rule (no push without --push unless live.autopush is chosen),
// the org identity signing the signals commit (D-581) and release taking
// the claim off the store's remote (D-582) are useStore's, unchanged. A
// code repo keeps openWorld's rules; a folder no store holds the id in
// keeps openWorld's refusal and its working next step.

import (
	"context"
	"path/filepath"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// openStoreWorld is the coordination world of store task id when the
// command runs outside every code repo (the home folder, a plain folder,
// a store clone). ok=false hands the command back to openWorld: a code
// repo, no id, or no store of this machine holding the id.
func (h standaloneCoord) openStoreWorld(ctx context.Context, id string) (w *coordWorld, ok bool, err error) {
	if id == "" || inCodeRepo(ctx, h.a.opts.RepoDir) {
		return nil, false, nil
	}
	return h.storeWorldOf(ctx, id)
}

// inCodeRepo reports whether dir is inside a code repo: a git work tree
// that is no store clone.
func inCodeRepo(ctx context.Context, dir string) bool {
	if _, inClone := storeHere(dir); inClone {
		return false
	}
	return insideWorkTree(ctx, dir)
}

// storeWorldOf is the world of store task id wherever the command runs:
// the org-store folder holding it, found by the id scan. A code repo's
// heartbeat with no --id reaches the claims taken from home through it
// (devco:D-666). ok=false: no store of this machine holds the id.
func (h standaloneCoord) storeWorldOf(ctx context.Context, id string) (w *coordWorld, ok bool, err error) {
	a := h.a
	look := &rowLookup{id: id}
	hold, found := a.rowFolderIn(ctx, a.orgFolders(ctx), id, look)
	if !found {
		hold, found = a.rowFolderIn(ctx, a.everyOrgFolders(ctx), id, look)
	}
	if !found {
		return nil, false, nil
	}
	cfg, _, cerr := orgcfg.LoadConfig(a.opts.Home)
	if cerr != nil || cfg == nil {
		return nil, false, nil
	}
	org := cfg.FindOrg(a.orgOfRow(ctx, id))
	if org == nil {
		return nil, false, nil
	}
	w, err = h.storeWorldOn(ctx, cfg, org, hold, a.companyFolderOf(ctx, hold)...)
	return w, true, err
}

// storeWorldOn is the coordination world on org store folder hold: the
// App opened store-only on it (also attached beside it for reads), the
// caller as the person's identity in that store's org, the claims on the
// store clone's signals ref under hold's folder (useStore).
func (h standaloneCoord) storeWorldOn(ctx context.Context, cfg *orgconf.Config, org *orgconf.Org, hold attach.Store, also ...attach.Store) (*coordWorld, error) {
	a := h.a
	if err := a.openStoreOnlyOn(ctx, hold, hold.Name, also...); err != nil {
		return nil, err
	}
	who, actor, err := a.storeCaller(ctx, hold.Name)
	if err != nil {
		return nil, err
	}
	if who.Kind != identity.KindAgent && who.Kind != identity.KindHuman {
		return nil, errs.New(errs.IdentityFailed, "coordination needs an agent actor or a person on the roster").
			WithHint("start an agent session with `lore agent start --label <name>`, or set your org identity")
	}
	if who.Kind == identity.KindAgent && who.Owner == nil {
		if err = a.requireRosterAgent(ctx, who.StableKey); err != nil {
			return nil, err
		}
	}
	dir, _, _, isClone := a.storeClone(ctx, hold.Name)
	if !isClone {
		return nil, errs.New(errs.Unsupported, "the store holding "+string(hold.Name)+" is not a git clone here: its signals ref cannot be reached").
			WithHint("`lore store list` lists the stores this machine knows")
	}
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "open the store clone", err)
	}
	w := &coordWorld{
		a: a, repo: repo, stateDir: filepath.Join(gitbackend.StateDirOf(dir), signalsStateDir),
		folder: signalsRootFolder, who: who, actorID: actor.String(),
		sit:      orgresolve.Result{Org: org},
		orgStore: hold.Name,
	}
	w.settings(cfg, org)
	w.remoteName = gitbackend.RemoteName
	w.remote = &gitremote.Git{Repo: repo, Name: gitbackend.RemoteName}
	if err = w.useStore(ctx); err != nil {
		return nil, err
	}
	w.remote = w.spot.remote
	return w, nil
}

// storeCaller is who coordinates on store name: the person's identity in
// the org owning that store (the open's per-store writer, orgstoreactor.go)
// when it differs from the folder's own, else the folder's. Why: inside a
// store clone the folder's identity chain lands on the OS user, so a
// release there was not the holder of the claim taken from home as the
// org identity (E_LOST_CLAIM).
func (a *App) storeCaller(ctx context.Context, name store.Source) (identity.Resolved, ids.ID[ids.Actor], error) {
	st, err := a.rowState(ctx)
	if err != nil {
		return identity.Resolved{}, ids.ID[ids.Actor]{}, err
	}
	if sw, ok := st.storeWho[name]; ok {
		return sw.who, sw.id, nil
	}
	actor, err := a.Actor(ctx)
	if err != nil {
		return identity.Resolved{}, ids.ID[ids.Actor]{}, err
	}
	return st.who, actor, nil
}
