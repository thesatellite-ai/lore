package wire

// lensfolders.go — `lore sql` over every folder of each attached store
// (devco D-443). A store attached whole (no folder: the org's company
// store, a stores.yaml entry with no `folder:`) opens its root folder only, so
// in a repo attaching the company store whole `rules_all` showed the
// repo's own folder and nothing of the company's: the store's rows live
// in its folders. For the one query the lens attaches each folder of such
// a store, read-only, named <store>/<folder>, unless another attachment
// already reads that place (one source per place: lensSources drops a
// second name on one cache file).
//
//	attached whole: acme (root) ─▶ + acme/sync, acme/ops (from the clone)
//	attached:       acme/org    ─▶ the place acme/org is read once, as acme/org

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/store"
)

// lensFolders is the folders of the stores this repo attaches whole that
// no attachment reads yet: what the sql lens attaches beside them. The
// personal store (me) is not expanded: its folders are other projects'
// personal notes. None outside a code repo (the store-only opens choose
// their folders themselves, scope.go), and none on a store-only open
// anywhere (--store <org>[/<folder>], D-473): that open chose the read's
// folders, and the repo's attachments are not its scope.
func (a *App) lensFolders(ctx context.Context) []attach.Store {
	if _, in := storeHere(a.opts.RepoDir); in {
		return nil
	}
	a.mu.Lock()
	storeOnly := a.st != nil && a.st.storeOnly
	a.mu.Unlock()
	if storeOnly {
		return nil
	}
	top, err := a.storeRepoTop(ctx)
	if err != nil || top == "" {
		return nil
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return nil
	}
	a.mu.Lock()
	extra := slices.Clone(a.extra)
	a.mu.Unlock()
	org := a.orgStoresHere(ctx)
	attached := slices.Concat(cfg.Stores, org, extra)
	// whole is what may widen: the org entry's own attach of this repo
	// (the bare org name: an owned repo's store root, or its linked
	// folder) is that one place, never the whole store (devco D-497).
	whole := slices.Concat(cfg.Stores, slices.DeleteFunc(slices.Clone(org), orgEntryAttach), extra)
	seen := map[string]bool{}
	names := map[store.Source]bool{} // a name in use elsewhere would refuse the open ("attached twice")
	for _, s := range attached {
		names[s.Name] = true
		if loc, lerr := attachLocation(s, s.Folder); lerr == nil {
			seen[loc] = true
		}
	}
	var out []attach.Store
	for _, s := range whole {
		if (s.Folder != "" && s.Folder != rowfs.FolderRoot) || s.Name == attach.ReservedMe || s.URL == "" {
			continue
		}
		dir, derr := a.storeDir(ctx, s)
		if derr != nil {
			continue // a store not cloned here has no folders to read
		}
		folders, ferr := rowfs.StoreFolders(dir)
		if ferr != nil {
			continue
		}
		for _, f := range folders {
			loc, lerr := attachLocation(s, f)
			name := store.Source(string(s.Name) + targetSep + f)
			if lerr != nil || seen[loc] || names[name] {
				continue
			}
			seen[loc], names[name] = true, true
			out = append(out, attach.Store{Name: name, URL: s.URL, Folder: f, Branch: s.Branch, ReadOnly: true})
		}
	}
	return out
}

// orgEntryAttach reports whether s is the org entry's attach of this repo
// (orgresolve's first store: the bare org name), not the org's company
// store (<org>/<org_folder>). An owned repo with no link attaches the
// store's root folder through it, and STORE TARGETS shows a folder of the
// store only in repos that attach it, so lists, search and sql there read
// the root alone; widening it listed every folder's rows (devco D-497).
func orgEntryAttach(s attach.Store) bool {
	return !strings.Contains(string(s.Name), targetSep)
}

// withExtra runs fn with extra attached (unrecorded) and puts the App's
// attachments back afterwards, so a long-lived App (`lore api`) reads the
// usual stores again after the one query.
//
// The state open before the call is set aside, never closed: fn runs on a
// state of its own (opened with extra by the first read in fn) and only
// that one is closed, then the earlier state comes back as it was. A
// caller in the middle of a write holds the earlier state's engine, and
// the leak guard of a client repo runs withExtra from inside that write
// (Create → createFrame → guard → LeakCheckDocs): closing the earlier
// state there closed the repo's git backend under the write, so `lore
// rule add --to repo` in such a repo failed E_INTERNAL "git: closed"
// (H-176, devco:D-918).
func (a *App) withExtra(extra []attach.Store, fn func() error) error {
	if len(extra) == 0 {
		return fn()
	}
	a.mu.Lock()
	before := slices.Clone(a.extra)
	held, heldOpened, heldErr := a.st, a.opened, a.err
	a.st, a.opened, a.err = nil, false, nil
	a.extra = append(a.extra, extra...)
	a.mu.Unlock()
	err := fn()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extra = before
	if a.st != nil {
		_ = a.st.close() // fn's own state: the held one comes back below
	}
	a.st, a.opened, a.err = held, heldOpened, heldErr
	return err
}
