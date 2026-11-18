package wire

// personalread_noproject.go — devco P-4: the knowledge reads in a git
// repo lore is not set up in (no .lore/, not linked by its remote), on
// a machine with a personal store. A write naming `--to me` there already
// runs on the personal store alone (targetoutside.go), so a solo writer's
// rules for a docs-only repo (one that must stay free of lore files on
// GitHub) were written, yet every read there refused "lore isn't set up"
// and pointed at `lore init`, which adds the very files the repo must not
// hold. The reads are served the way the write is:
//
//	runPersonalReadNoProject ── not a knowledge read / project here ─▶ usual path
//	                         ├─ no personal store set up ─▶ usual path (its refusal)
//	                         └─ the personal store alone ─▶ a opened on `me`, eng.Run
//
// Nothing is created in the repo (the store-only open has no repo store).

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/op"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
)

// personalReadNouns are the knowledge nouns whose list, show and search
// a repo with no lore project serves from the personal store.
var personalReadNouns = map[string]bool{
	"rule": true, "memory": true, "decision": true, "pattern": true, "behaviour": true, "prompt": true,
	"playbook": true, "hotfix": true, "incident": true, "mission": true, "plan": true, "task": true,
	"tasklist": true, "handoff": true, "snapshot": true, "techdoc": true, "architecturenote": true,
	"cookbookrecipe": true, "tastepref": true, "workflow": true, "workspace": true, "reminder": true,
	"tag": true, "comment": true,
}

// personalReadVerbs are the read verbs of personalReadNouns served there.
var personalReadVerbs = map[string]bool{"list": true, "show": true, "search": true}

// personalReadOp reports whether name is a knowledge read served from the
// personal store in a repo with no lore project.
func personalReadOp(name string) bool {
	if name == opssearch.OpRun {
		return true
	}
	noun, verb, ok := strings.Cut(name, ".")
	return ok && personalReadNouns[noun] && personalReadVerbs[verb]
}

// runPersonalReadNoProject runs a knowledge read on a store-only App over
// the personal store when the command runs in a git repo with no lore
// project; handled is false when the command takes the usual path.
func (a *App) runPersonalReadNoProject(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (res op.Result, handled bool, err error) {
	if e.Spec().Access != op.AccessRead || !personalReadOp(e.Spec().Name) || !insideWorkTree(ctx, a.opts.RepoDir) || !readWithoutProject(ctx, a.opts) {
		return op.Result{}, false, nil
	}
	me, found, _, err := a.baseStore(ctx, attach.ReservedMe)
	if err != nil || !found {
		return op.Result{}, false, nil // no personal store: the usual path says the repo is not set up
	}
	if c := a.clientRepo(ctx); c.Org != "" && c.Folder == "" && a.opts.Notice != nil {
		a.opts.Notice(clientPersonalOnlyNote(c))
	}
	// the operations call back into a (its Env), so a itself is opened on
	// the personal store, which answers for the repo store as it does for
	// the `--to me` write here
	if err := a.openStoreOnlyOn(ctx, me, me.Name); err != nil {
		return op.Result{}, true, err
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return op.Result{}, true, err
	}
	res, err = eng.Run(ctx, e, d, c)
	return res, true, err
}

// clientPersonalOnlyNote is the line a read in a client repo no folder is
// linked to prints when only the personal store answered it (devco:D-610:
// search there said "(no matches)" with no word that the org's folders
// were not read, while the same read without a personal store refused
// naming the link and --store <org>/<folder>). It names the same steps.
func clientPersonalOnlyNote(c ClientRepo) string {
	return "read your personal store only, not " + c.Org + "'s folders: " + clientRepoStep(c)
}
