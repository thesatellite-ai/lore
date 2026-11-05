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

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// cloneWorkDir is the worktree lore owns inside a clone base (spec 6.1).
const cloneWorkDir = "work"

// StoreStatus implements opsstores.StatusReader from local reads only.
// A marker without a visibility reads as internal (CFG4 default).
func (a *App) StoreStatus(_ context.Context, e opsstores.Entry) opsstores.Status {
	canon, err := canonical(attach.Store{URL: e.URL})
	if err != nil {
		return opsstores.Status{Reach: opsstores.ReachMissing}
	}
	u, err := url.Parse(canon)
	if err != nil {
		return opsstores.Status{Reach: opsstores.ReachMissing}
	}
	if u.Scheme == folder.Scheme {
		if v, ok := markerVisibility(filepath.FromSlash(u.Path)); ok {
			return opsstores.Status{Visibility: v, Reach: opsstores.ReachOK}
		}
		return opsstores.Status{Reach: opsstores.ReachMissing}
	}
	r, err := git.RemoteOf(u)
	if err != nil {
		return opsstores.Status{Reach: opsstores.ReachNoLocalCopy}
	}
	base, err := git.CloneBase(a.opts.Home, r)
	if err != nil {
		return opsstores.Status{Reach: opsstores.ReachNoLocalCopy}
	}
	if e.Branch != "" {
		base += "@" + filepath.FromSlash(e.Branch) // git's branchCloneSep (clone.go)
	}
	if v, ok := markerVisibility(filepath.Join(base, cloneWorkDir)); ok {
		return opsstores.Status{Visibility: v, Reach: opsstores.ReachLocalCopy}
	}
	return opsstores.Status{Reach: opsstores.ReachNoLocalCopy}
}

// markerVisibility reads dir's lore-store.yaml visibility; ok=false when
// there is no readable marker.
func markerVisibility(dir string) (string, bool) {
	m, err := rowfs.ReadMarker(dir)
	if err != nil {
		return "", false
	}
	if m.Visibility == "" {
		return string(rowfs.VisibilityInternal), true
	}
	return string(m.Visibility), true
}

var _ opsstores.StatusReader = (*App)(nil)
