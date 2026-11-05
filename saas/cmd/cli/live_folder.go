// live_folder.go — which live folder the repo store's backend writes to.
//
// The repo store opens one lore project: the repository's top-level
// .lore/data, or a monorepo subproject's <sub>/.lore/data (git.OpenRepo
// roots the backend at the nearest folder holding .lore). Its live rows go
// to that project's folder in the live tree (`_root` for the top level,
// the project's repo-relative path otherwise, live.FolderFor), so a landing
// writes them back to <sub>/.lore/data and one ride commit carries every
// project's waiting rows (v0.1.17, side effect 34).
package main

import (
	"os"
	"path/filepath"

	"saas/pkg/aicoder/live"
)

// gitDirEntry is the entry that marks a work tree's top level (a folder in
// a normal clone, a file in a linked worktree or submodule).
const gitDirEntry = ".git"

// liveRepoPlace returns the git top level holding the project folder dir
// and the project's live folder. A folder outside any work tree keeps
// (dir, live.RootFolder): live mode does not apply there and the session
// lookup returns nil anyway.
func liveRepoPlace(dir string) (top, folder string) {
	real := dir
	if abs, err := filepath.Abs(dir); err == nil {
		real = abs
	}
	if r, err := filepath.EvalSymlinks(real); err == nil {
		real = r
	}
	for d := real; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, gitDirEntry)); err == nil {
			rel, err := filepath.Rel(d, real)
			if err != nil {
				return dir, live.RootFolder
			}
			return d, live.FolderFor(filepath.ToSlash(rel))
		}
		if filepath.Dir(d) == d {
			return dir, live.RootFolder
		}
	}
}
