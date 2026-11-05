package main

// initsub.go — `lore init` roots the project at the folder it runs in
// (OWNERQ-1, v0.1.17 projresolve: the project root is the working folder).
//
// Every other command resolves its project from the nearest `.lore/` at or
// above the working folder, up to the git top (adapters/backend/git
// projectRoot), so in a monorepo `cd web && lore task add …` reads and writes
// web/.lore/data and the live rows ride under the `web/` folder of the one
// refs/lore/live (wire/host livePlace). Init is the one command that must not
// walk up: run in a sub-folder of an initialised repo it makes a separate
// project there, as v0.1.17 did, instead of adopting the parent's. Every
// project of one checkout shares one repo id (G-MONO-1, sharedRepoMeta). It does so
// by creating `<wd>/.lore` before the repo store opens, which makes wd the
// nearest project root; if init then fails before anything is written there,
// the empty folder is removed again.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
)

// initLoreDirMode is the mode of a `.lore` folder init creates; the same
// owner-and-group mode rowfs gives the folders it creates.
const initLoreDirMode = 0o750

// rootInitAt makes wd the root the repo store opens for `lore init`:
// when wd has no `.lore` folder it creates one (any `.lore` above wd would
// otherwise be taken for the project). The returned undo removes a folder
// rootInitAt created when it is still empty; it is a no-op otherwise.
// A `.lore` that exists but is not a folder is refused (E_INVALID_INPUT):
// writing a project there would fail later with a less clear error.
func rootInitAt(wd string) (undo func(), err error) {
	noop := func() {}
	dir := filepath.Join(wd, rowfs.LoreDir)
	fi, err := os.Lstat(dir)
	switch {
	case err == nil && fi.IsDir():
		return noop, nil
	case err == nil:
		return noop, errs.New(errs.InvalidInput, "init: "+dir+" exists and is not a folder")
	case !errors.Is(err, os.ErrNotExist):
		return noop, errs.Wrap(errs.Internal, "init: read "+dir, err)
	}
	if err := os.Mkdir(dir, initLoreDirMode); err != nil {
		return noop, errs.Wrap(errs.Internal, "init: create "+dir, err)
	}
	return func() {
		_ = os.Remove(dir) // only an empty folder goes; anything init wrote stays for the person to see
	}, nil
}

// repoMetaPathspec is every project's _meta.json in a checkout, the top
// project's included (a leading `**/` also matches no folder at all).
const repoMetaPathspec = ":(glob)**/" + rowfs.RepoMetaPath

// mergeIDsHint is the way out of two repo ids in one checkout.
const mergeIDsHint = "one repository has one repo id (PROVENANCE 11.6): keep one, then run `lore repo merge-ids` in each project that carried the other"

// checkoutRepoMetas is the repo identity of every lore project in wd's git
// checkout except the one rooted at wd, keyed by its folder relative to the
// checkout top ("." for the top). The projects come from git's own file list
// (tracked, plus untracked files git does not ignore, so a project made but
// not yet committed counts), never from walking the folders (G-MONO-1). Not
// a git checkout: none. A _meta.json that cannot be read refuses (an
// unreadable id must not let init mint a second one).
func checkoutRepoMetas(ctx context.Context, wd string) (map[string]rowfs.RepoMeta, error) {
	git := gitref.Exec{}
	out, err := git.Run(ctx, gitref.Invocation{Dir: wd, Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		return nil, nil // not inside a git checkout: no repo to share an id with
	}
	top := strings.TrimSpace(string(out))
	list, err := git.Run(ctx, gitref.Invocation{Dir: top, Args: []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", repoMetaPathspec}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "init: list the projects of this checkout", err)
	}
	self := realPath(wd)
	metas := map[string]rowfs.RepoMeta{}
	for _, p := range strings.Split(string(list), "\x00") {
		if p == "" {
			continue
		}
		rel := strings.TrimSuffix(strings.TrimSuffix(p, rowfs.RepoMetaPath), "/")
		if rel == "" {
			rel = "."
		}
		dir := filepath.Join(top, filepath.FromSlash(rel))
		if realPath(dir) == self || metas[rel].RepoID != "" {
			continue
		}
		m, exists, rerr := rowfs.ReadRepoMeta(dir)
		if rerr != nil {
			return nil, errs.Wrap(errs.InvalidInput, "init: read the repo id of the project in "+rel, rerr)
		}
		if exists && m.RepoID != "" {
			metas[rel] = m
		}
	}
	return metas, nil
}

// realPath is p with symlinks resolved (p itself when that fails).
func realPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// chooseRepoMeta is the one repo identity the projects of a checkout share
// (G-MONO-1): ok false when none has an id; an id listed in another
// project's repo_id_aliases is that project's id (PROVENANCE 11.9); two ids
// left refuse with E_AMBIGUOUS_ID naming each and where it is.
func chooseRepoMeta(metas map[string]rowfs.RepoMeta) (rowfs.RepoMeta, bool, error) {
	aliased := map[string]bool{}
	for _, m := range metas {
		for _, a := range m.Aliases {
			aliased[a] = true
		}
	}
	folders := make([]string, 0, len(metas))
	for f := range metas {
		folders = append(folders, f)
	}
	sort.Strings(folders)
	var pick rowfs.RepoMeta
	where := map[string]string{}
	var order []string
	for _, f := range folders {
		m := metas[f]
		if aliased[m.RepoID] {
			continue
		}
		if _, seen := where[m.RepoID]; !seen {
			where[m.RepoID] = f
			order = append(order, m.RepoID)
			if len(order) == 1 {
				pick = m
			}
		}
	}
	switch len(order) {
	case 0:
		return rowfs.RepoMeta{}, false, nil
	case 1:
		return pick, true, nil
	}
	parts := make([]string, len(order))
	for i, id := range order {
		parts[i] = id + " (" + where[id] + ")"
	}
	return rowfs.RepoMeta{}, false, errs.New(errs.AmbiguousID, "init: this checkout's projects carry different repo ids: "+strings.Join(parts, ", ")+"; nothing written").WithHint(mergeIDsHint)
}

// sharedRepoMeta is the repo identity a new project at wd takes:
// chooseRepoMeta over checkoutRepoMetas.
func sharedRepoMeta(ctx context.Context, wd string) (rowfs.RepoMeta, bool, error) {
	metas, err := checkoutRepoMetas(ctx, wd)
	if err != nil {
		return rowfs.RepoMeta{}, false, err
	}
	return chooseRepoMeta(metas)
}

// inheritRepoID gives the new project the checkout's repo identity (up,
// from sharedRepoMeta) when its own _meta.json has no repo_id yet, so
// EnsureRepoIdentity adopts that id instead of minting one.
func (h *host) inheritRepoID(ctx context.Context, up rowfs.RepoMeta) error {
	own, exists, err := h.app.ReadRepoMeta(ctx)
	if err != nil || !exists || own.RepoID != "" {
		return err
	}
	own.RepoID, own.URLs, own.Aliases, own.ForkedFrom, own.SplitFrom = up.RepoID, up.URLs, up.Aliases, up.ForkedFrom, up.SplitFrom
	return h.app.WriteRepoMeta(ctx, own)
}
