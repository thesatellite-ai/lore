package wire

// sync_orgstores.go — `lore sync` also sends the waiting commits of the
// org stores this machine's ~/.lore/config.yaml lists, not only the stores
// the repo attaches (plainfiles:4). A write with --to <org store>/<folder>
// from a repo that does not attach that store said "committed locally to
// store …; lore sync sends it", and `lore doctor` said "`lore sync` pushes
// it", but `lore sync` synced only the attached stores: the commits stayed
// in the clone and teammates never got them.
//
//	SyncStores("") ─▶ SyncAll (attached stores, as before)
//	              ─▶ pushWaitingOrgStores: each configured org store whose
//	                 clone still holds unpushed commits ─▶ PushStoreNow
//	                 (the `lore store push` path) ─▶ one more report line
//
// Only clones with waiting work are touched: an org store with nothing to
// send costs no fetch.

import (
	"context"
	"path"
	"slices"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/repourl"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// pushWaitingOrgStores pushes every configured org store clone that still
// holds commits its remote has not got, after the attached stores synced.
// A config that does not load is no report (`lore config check` names it).
func (a *App) pushWaitingOrgStores(ctx context.Context) []opsstores.Report {
	if a.opts.Home == "" {
		return nil
	}
	c := &standaloneClones{home: a.opts.Home, now: a.opts.Wall}
	list, err := c.configuredStores()
	if err != nil {
		return nil
	}
	cl := c.clonesFor()
	var out []opsstores.Report
	for _, cs := range list {
		info := cl.Inspect(ctx, cs.norm)
		if !info.Cloned || !slices.ContainsFunc(info.Unpushed, isBranchRef) {
			continue // only a branch's commits wait for a push: refs/lore/backup/* stay local by design
		}
		// The branch whose clone folder is the one holding the work: an
		// org entry names the effective branch ("main"), while the clone
		// of the default branch has no branch pin in its folder name.
		branch, found := "", false
		for _, br := range append([]string{""}, cs.branches...) {
			if d, _, derr := gitbackend.CloneDirOf(a.opts.Home, cs.url, br); derr == nil && sameFile(d, info.Dir) {
				branch, found = br, true
				break
			}
		}
		if !found {
			continue // a clone lore's own store layout does not name: `lore store doctor` reports it
		}
		r := opsstores.Report{Store: orgStoreShortName(cs.norm)}
		res, perr := a.PushStoreNow(ctx, opsstores.PushIn{Store: cs.url, Branch: branch})
		switch {
		case perr != nil:
			r.Problems = []string{perr.Error()}
		default:
			r.Sent, r.Offline = res.Sent, res.Offline
		}
		out = append(out, r)
	}
	return out
}

// orgStoreShortName is the name a store of an org goes by when its
// lore-org.yaml gives none: the repo name of its url without .git.
func orgStoreShortName(norm string) string {
	return strings.TrimSuffix(path.Base(repourl.Mask(norm)), ".git")
}

// isBranchRef reports a local branch (refs/heads/…): the refs a store push
// sends. Inspect also lists lore's local refs (a compaction's backup),
// which no push ever sends.
func isBranchRef(ref string) bool { return strings.HasPrefix(ref, "refs/heads/") }
