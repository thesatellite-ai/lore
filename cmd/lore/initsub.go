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
// project there, as v0.1.17 did, instead of adopting the parent's. It does so
// by creating `<wd>/.lore` before the repo store opens, which makes wd the
// nearest project root; if init then fails before anything is written there,
// the empty folder is removed again.

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
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

// ancestorRepoMeta is the repo identity of the nearest project above wd in
// the same git checkout: the first folder from wd's parent up to the git top
// (the folder holding `.git`) whose `.lore/data/_meta.json` names a repo_id.
// Why: a monorepo has one repo id (PROVENANCE 11.6, "sub-projects are not
// repos"), so a sub-project init takes the id the repo already has instead
// of minting a second one. ok is false when no folder above has one, when wd
// is not inside a git checkout, or when a _meta.json cannot be read (init
// then mints, as for a repo's first project; nothing is guessed).
func ancestorRepoMeta(wd string) (rowfs.RepoMeta, bool) {
	abs, err := filepath.Abs(wd)
	if err != nil {
		return rowfs.RepoMeta{}, false
	}
	if real, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = real
	}
	top := ""
	for d := abs; top == ""; d = filepath.Dir(d) {
		if isGitTop(d) {
			top = d
		} else if filepath.Dir(d) == d {
			return rowfs.RepoMeta{}, false // not inside a git checkout: no repo to share an id with
		}
	}
	if top == abs {
		return rowfs.RepoMeta{}, false // the repo's top project has nothing above it
	}
	for d := filepath.Dir(abs); ; d = filepath.Dir(d) {
		if m, exists, rerr := rowfs.ReadRepoMeta(d); rerr == nil && exists && m.RepoID != "" {
			return m, true
		}
		if d == top {
			return rowfs.RepoMeta{}, false
		}
	}
}

// isGitTop reports whether d holds a `.git` entry (a folder, or the file of
// a worktree or submodule): the top of a checkout, where the walk stops.
func isGitTop(d string) bool {
	_, err := os.Lstat(filepath.Join(d, ".git"))
	return err == nil
}

// inheritRepoID gives a new sub-project the repo identity of the project
// above it (ancestorRepoMeta) when its own _meta.json has no repo_id yet, so
// EnsureRepoIdentity adopts that id instead of minting one. A no-op at the
// repo top, with no project above, or when the id is already set.
func (h *host) inheritRepoID(ctx context.Context, wd string) error {
	up, ok := ancestorRepoMeta(wd)
	if !ok {
		return nil
	}
	own, exists, err := h.app.ReadRepoMeta(ctx)
	if err != nil || !exists || own.RepoID != "" {
		return err
	}
	own.RepoID, own.URLs, own.Aliases, own.ForkedFrom, own.SplitFrom = up.RepoID, up.URLs, up.Aliases, up.ForkedFrom, up.SplitFrom
	return h.app.WriteRepoMeta(ctx, own)
}
