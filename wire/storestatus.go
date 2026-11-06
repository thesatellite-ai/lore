package wire

// store list status (CLI_REFERENCE 3.21). Flow:
//
//	StoreStatus(entry) → canonical url
//	  file://  → marker in the folder? ok + its visibility : missing
//	  git      → clone worktree (<home>/clones/<host>/<repo>[@branch]/work)
//	             holds the marker? local_copy + visibility : no_local_copy
//	No network and no backend open: store list never waits on a remote.

import (
	"context"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// cloneWorkDir is the worktree lore owns inside a clone base (spec 6.1).
const cloneWorkDir = "work"

// localStoreDir is the folder holding e's marker on this machine: the
// file:// folder, or the git clone's worktree; git=true for a git store.
func (a *App) localStoreDir(e opsstores.Entry) (dir string, isGit bool, err error) {
	canon, err := canonical(attach.Store{URL: e.URL})
	if err != nil {
		return "", false, err
	}
	u, err := url.Parse(canon)
	if err != nil {
		return "", false, errs.New(errs.StoreScheme, "wire: the store url does not parse")
	}
	if u.Scheme == folder.Scheme {
		return filepath.FromSlash(u.Path), false, nil
	}
	r, err := git.RemoteOf(u)
	if err != nil {
		return "", true, err
	}
	base, err := git.CloneBase(a.opts.Home, r)
	if err != nil {
		return "", true, err
	}
	if e.Branch != "" {
		base += "@" + filepath.FromSlash(e.Branch) // git's branchCloneSep (clone.go)
	}
	return filepath.Join(base, cloneWorkDir), true, nil
}

// StoreStatus implements opsstores.StatusReader from local reads only.
// A marker without a visibility reads as internal (CFG4 default).
func (a *App) StoreStatus(ctx context.Context, e opsstores.Entry) opsstores.Status {
	if e.Source == opsstores.SourceOrg && e.URL == redactedText {
		// ListStores hid an org store's url from quiet output (G-CORE-8):
		// read the status from the url the org gives that name.
		top, _ := a.repoTop(ctx) // no top: no renamed clash names to match
		for _, s := range orgStores(ctx, a.opts, top) {
			if string(s.Name) == e.Name {
				e.URL = s.URL
				break
			}
		}
	}
	dir, isGit, err := a.localStoreDir(e)
	if err != nil {
		if isGit {
			return opsstores.Status{Reach: opsstores.ReachNoLocalCopy}
		}
		return opsstores.Status{Reach: opsstores.ReachMissing}
	}
	v, ok := markerVisibility(dir)
	switch {
	case ok && isGit:
		return opsstores.Status{Visibility: v, Reach: opsstores.ReachLocalCopy}
	case ok:
		return opsstores.Status{Visibility: v, Reach: opsstores.ReachOK}
	case isGit:
		return opsstores.Status{Reach: opsstores.ReachNoLocalCopy}
	}
	return opsstores.Status{Reach: opsstores.ReachMissing}
}

// ReposUsingStore implements opsstores.RepoUser: the repo tops whose FLAG3
// use record lists a cache of the store name (cache path
// stores/<sto_id or name>/…; a combined cache by its source name), sorted.
func (a *App) ReposUsingStore(ctx context.Context, name string) ([]string, error) {
	list, err := a.ListStores(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(list, func(e opsstores.Entry) bool { return e.Name == name })
	if i < 0 {
		return nil, errs.New(errs.NotFound, "store visibility: no store named "+name+" is attached")
	}
	dir, _, err := a.localStoreDir(list[i])
	if err != nil {
		return nil, err
	}
	stem := name // a v0.1.17 marker without an id: the cache uses the name
	if m, merr := rowfs.ReadMarker(dir); merr == nil && !m.ID.IsZero() {
		stem = m.ID.String()
	}
	recs, err := sqlite.ReadUses(filepath.Join(a.opts.Home, cacheDir))
	if err != nil {
		return nil, err
	}
	prefix := cacheStoresDir + "/" + stem + "/"
	var out []string
	for _, rf := range recs {
		if rf.Record.Repo == "" || slices.Contains(out, rf.Record.Repo) {
			continue
		}
		if slices.ContainsFunc(rf.Record.Caches, func(u sqlite.Use) bool {
			return strings.HasPrefix(u.Path, prefix) || (strings.HasPrefix(u.Path, combinedDir+"/") && u.Store == name)
		}) {
			out = append(out, rf.Record.Repo)
		}
	}
	slices.Sort(out)
	return out, nil
}

// markerVisibility reads dir's lore-store.yaml visibility; ok=false when
// there is no readable marker.
func markerVisibility(dir string) (string, bool) {
	m, err := rowfs.ReadMarker(dir)
	if err != nil {
		return "", false
	}
	return visibilityOf(m), true
}

// visibilityOf is the marker's visibility, internal when unset (CFG4).
func visibilityOf(m rowfs.Marker) string {
	if m.Visibility == "" {
		return string(rowfs.VisibilityInternal)
	}
	return string(m.Visibility)
}

var (
	_ opsstores.StatusReader = (*App)(nil)
	_ opsstores.RepoUser     = (*App)(nil)
)
