package renderhost

// render_private_refresh.go — keeping the private render current between
// explicit `lore render` runs (devco:D-921).
//
// The private store files (~/.lore/render/<repo>/<store>.md) are what a
// tool's private import file (CLAUDE.local.md and the like) loads into
// every session: the must rules and critical/high hotfixes of the repo's
// internal stores. Before this file only `lore render` and `lore sync`
// rewrote them, so a rule edited or archived with `lore rule edit` /
// `lore rule archive` stayed in the agent's context with its old text
// until someone rendered again (an agent answered from an archived rule).
//
//	a write in a checkout (app's write-render hook)  ─┐
//	the SessionStart hook (lore context --hook …)     ─┴─▶ refreshPrivateStale
//	    plan the private files (nothing written)
//	    for each file already on disk:
//	      stamp differs from the plan (a row or a setting changed) ─▶ rewrite it
//	      same, edited by hand, or missing                          ─▶ leave it
//
// Only files a render already wrote are refreshed: a repo whose person
// never rendered gets no new file and no import line from a plain write
// (that stays `lore render`'s job, with its report), and a file edited by
// hand is left for `lore doctor` to name, as the sync refresh does.
//
// The over-budget warning travels with the refresh (H-196, devco:D-935):
// render and sync warn only when they change a private file (D-573), so a
// write that refreshed the file silently took the warning away from every
// later sync. A refresh that rewrote a file returns the warning (to whoever
// may fix it, D-725) and the write prints it once on stderr.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	"github.com/thesatellite-ai/lore/wire"
)

// PrivateRefresh is what a refresh of the private render did.
type PrivateRefresh struct {
	// Wrote lists the private files rewritten.
	Wrote []string
	// OverBudget is the over-budget warning of the private context (one
	// decorated WARN line and its fix) when a file was rewritten and the
	// pinned part is over context.budget.internal in a way this person may
	// fix, else "". Render and sync warn only when they change a file
	// (D-573), so a write that refreshed it must say it (H-196).
	OverBudget string
}

// RefreshPrivateStale rewrites each private render file of the checkout at
// root that is on disk and whose rows changed since it was rendered
// (devco:D-921). It prints nothing (it runs after a write's own output and
// inside the SessionStart hook, whose stdout is the hook's JSON): the
// caller prints the returned over-budget warning where it may. An error
// leaves the files as they were.
func (h *Host) RefreshPrivateStale(ctx context.Context, root string) (PrivateRefresh, error) {
	return h.refreshPrivateStale(ctx, root)
}

// refreshPrivateStale is RefreshPrivateStale.
func (h *Host) refreshPrivateStale(ctx context.Context, root string) (PrivateRefresh, error) {
	var res PrivateRefresh
	if h.app == nil || root == "" || !h.privateRenderedHere() {
		return res, nil
	}
	if wire.RefuseWriteInReadOnlyHome(h.opts.Home) != nil {
		return res, nil // a read-only home keeps what it has, as render --yes refuses there
	}
	ctx = engine.WithAccess(ctx, op.AccessRead)
	p, err := h.planPrivate(ctx, root)
	if err != nil {
		return res, err
	}
	if len(p.stores) == 0 {
		return res, nil
	}
	for _, f := range p.files {
		stampIn := privateStampIn
		if filepath.Base(f.path) == privateIndexFile {
			stampIn = nil // the index has no stamp: any difference is stale
		}
		kind, verr := copyVerdict(f.path, f.body, stampIn)
		if verr != nil {
			return res, verr
		}
		if kind != opsrender.CopyStale {
			continue // current, edited by hand (doctor names it) or never rendered
		}
		if werr := atomicWriteFile(f.path, []byte(f.body), renderFileMode); werr != nil {
			return res, errs.FileIOOr("refresh the private render", f.path, werr)
		}
		res.Wrote = append(res.Wrote, f.path)
	}
	if len(res.Wrote) > 0 && p.overCoreWarns() {
		res.OverBudget = h.decor.warn(privateOverCoreWarn(p)) // as render and sync warn when they change a file
	}
	return res, nil
}

// RefreshPrivateStaleIn is RefreshPrivateStale for the checkout that holds
// dir (the project folder with .lore/ above it, else the git work tree top:
// a linked client repo has no .lore/); outside a checkout it does nothing.
func (h *Host) RefreshPrivateStaleIn(ctx context.Context, dir string) (PrivateRefresh, error) {
	return h.refreshPrivateStale(ctx, h.checkoutRootOf(ctx, dir))
}

// checkoutRootOf is the root the private render of dir's checkout is
// planned from ("" outside a git work tree).
func (h *Host) checkoutRootOf(ctx context.Context, dir string) string {
	if root, ok := orgcfg.ProjectRoot(ctx, dir, h.opts.Home); ok {
		return root
	}
	top, err := runGit(ctx, false, "-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(top))
}

// privateRenderedHere reports whether a private render folder of some
// project exists under the lore home: the cheap first check before a
// write plans the private files (a machine that never rendered pays
// nothing).
func (h *Host) privateRenderedHere() bool {
	entries, err := os.ReadDir(filepath.Join(h.opts.Home, renderDirName))
	return err == nil && len(entries) > 0
}
