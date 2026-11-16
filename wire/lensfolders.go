package wire

// lensfolders.go — `lore sql` over every folder of each attached store
// (devco D-443). A store attached whole (no folder: the org entry's store,
// a stores.yaml entry with no `folder:`) opens its root folder only, so
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

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/store"
)

// lensFolders is the folders of the stores this repo attaches whole that
// no attachment reads yet: what the sql lens attaches beside them. The
// personal store (me) is not expanded: its folders are other projects'
// personal notes. None outside a code repo (the store-only opens choose
// their folders themselves, scope.go).
func (a *App) lensFolders(ctx context.Context) []attach.Store {
	if _, in := storeHere(a.opts.RepoDir); in {
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
	attached := slices.Concat(cfg.Stores, a.orgStoresHere(ctx), extra)
	seen := map[string]bool{}
	for _, s := range attached {
		if loc, lerr := attachLocation(s, s.Folder); lerr == nil {
			seen[loc] = true
		}
	}
	var out []attach.Store
	for _, s := range attached {
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
			if lerr != nil || seen[loc] {
				continue
			}
			seen[loc] = true
			out = append(out, attach.Store{Name: store.Source(string(s.Name) + targetSep + f), URL: s.URL, Folder: f, Branch: s.Branch, ReadOnly: true})
		}
	}
	return out
}

// withExtra runs fn with extra attached (unrecorded) and puts the App's
// attachments back afterwards, so a long-lived App (`lore api`) reads the
// usual stores again after the one query.
func (a *App) withExtra(extra []attach.Store, fn func() error) error {
	if len(extra) == 0 {
		return fn()
	}
	a.mu.Lock()
	before := slices.Clone(a.extra)
	a.mu.Unlock()
	for _, x := range extra {
		a.attachExtra(x)
	}
	err := fn()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extra = before
	if a.st != nil {
		_ = a.st.close() // reopened by the next command with the usual stores
		a.st, a.opened, a.err = nil, false, nil
	} else if a.opened {
		a.opened, a.err = false, nil
	}
	return err
}
