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

// RefreshPrivateStale rewrites each private render file of the checkout at
// root that is on disk and whose rows changed since it was rendered
// (devco:D-921). It prints nothing (it runs after a write's own output and
// inside the SessionStart hook, whose stdout is the hook's JSON) and
// returns the files it rewrote; an error leaves the files as they were.
func (h *Host) RefreshPrivateStale(ctx context.Context, root string) ([]string, error) {
	return h.refreshPrivateStale(ctx, root)
}

// refreshPrivateStale is RefreshPrivateStale.
func (h *Host) refreshPrivateStale(ctx context.Context, root string) ([]string, error) {
	if h.app == nil || root == "" || !h.privateRenderedHere() {
		return nil, nil
	}
	if wire.RefuseWriteInReadOnlyHome(h.opts.Home) != nil {
		return nil, nil // a read-only home keeps what it has, as render --yes refuses there
	}
	ctx = engine.WithAccess(ctx, op.AccessRead)
	p, err := h.planPrivate(ctx, root)
	if err != nil {
		return nil, err
	}
	if len(p.stores) == 0 {
		return nil, nil
	}
	var wrote []string
	for _, f := range p.files {
		stampIn := privateStampIn
		if filepath.Base(f.path) == privateIndexFile {
			stampIn = nil // the index has no stamp: any difference is stale
		}
		kind, verr := copyVerdict(f.path, f.body, stampIn)
		if verr != nil {
			return wrote, verr
		}
		if kind != opsrender.CopyStale {
			continue // current, edited by hand (doctor names it) or never rendered
		}
		if werr := atomicWriteFile(f.path, []byte(f.body), renderFileMode); werr != nil {
			return wrote, errs.FileIOOr("refresh the private render", f.path, werr)
		}
		wrote = append(wrote, f.path)
	}
	return wrote, nil
}

// RefreshPrivateStaleIn is RefreshPrivateStale for the checkout that holds
// dir (the project folder with .lore/ above it, else the git work tree top:
// a linked client repo has no .lore/); outside a checkout it does nothing.
func (h *Host) RefreshPrivateStaleIn(ctx context.Context, dir string) ([]string, error) {
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
