package wire

// rosterread_noproject.go — `lore actor list` / `lore actor show` in a git
// repo lore is not set up in (no .lore/, not linked by its remote). A read
// there sets nothing up, so the repo's full open refuses it
// (doctor_nocreate.go); the roster verbs (team show, member list) are
// already served by the org store alone there (StoreOnlyRoster, R-12).
// The actor reads list the same people, so they are served the same way:
// an admin who just started a team from a fresh clone, or a member who
// joined from one, sees the org's people before anyone ran lore init
// (owner's journey: start a team, then `lore actor list` in the repo).
//
//	runRosterReadNoProject ── not an actor read / not a read / project here ─▶ usual path
//	                       ├─ no org for this folder ─▶ usual path (its refusal)
//	                       └─ the org's roster store ─▶ a opened on it alone, eng.Run

import (
	"context"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/store"
)

// rosterReadOps are the reads of the people an org's roster holds, served
// by the org store in a repo with no lore project.
var rosterReadOps = map[string]bool{
	infra.OpActorList: true,
	infra.OpActorShow: true,
}

// runRosterReadNoProject runs an actor read on a store-only App over the
// org's roster store when the command runs in a git repo with no lore
// project; handled is false when the command takes the usual path (any
// other op, a write, a set-up repo, a folder with no org).
func (a *App) runRosterReadNoProject(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (res op.Result, handled bool, err error) {
	if !rosterReadOps[e.Spec().Name] || !insideWorkTree(ctx, a.opts.RepoDir) || !readWithoutProject(ctx, a.opts) {
		return op.Result{}, false, nil
	}
	r, err := a.repoRoster(ctx)
	if err != nil || r.src == store.SourceRepo {
		return op.Result{}, false, nil // no org here: the usual path says the repo is not set up
	}
	one, err := orgStoreNamed(ctx, a.opts, r.src)
	if err != nil {
		return op.Result{}, true, err
	}
	// the operations call back into a (its Env), so a itself is opened on
	// the roster store; it answers for no repo ("": the roster rows name no
	// project folder), and nothing is created in the repo
	if err := a.openStoreOnlyOn(ctx, one, ""); err != nil {
		return op.Result{}, true, err
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return op.Result{}, true, err
	}
	res, err = eng.Run(ctx, e, d, c)
	return res, true, err
}
