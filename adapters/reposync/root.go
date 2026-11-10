package reposync

// root.go — which lore project a command run in some folder belongs to
// (E-FATES-6). Every reposync entry point (install-action, the per-command
// pin refresh, peek, promote, ci-merge) names its files by the project's
// folder relative to the work tree root (PathPrefix): a project in svc/api
// owns lore-sync-merge-svc-api.yml and svc/api/.lore/data. Run from a plain
// subfolder of the project (svc/api/internal), the working directory is not
// that folder, so it must be resolved first, or install-action writes a
// second workflow named after the subfolder and peek/promote look for
// .lore/data where there is none.
//
//	dir ─▶ dir/.lore? ─▶ parent/.lore? ─▶ … up to the work tree root
//	        first folder holding .lore/ is the project; none: dir as given

import (
	"os"
	"path/filepath"
	"strings"
)

// loreDirName is the folder that marks a lore project.
const loreDirName = ".lore"

// projectDir is the lore project dir belongs to: the nearest folder from dir
// up to top (the work tree root, inclusive) that holds a .lore/ folder. When
// none does, or dir is outside top, dir is returned unchanged, so the
// callers' own "not a lore project" answers stay as they were.
func projectDir(top, dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	if real, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = real
	}
	root := top
	if real, evalErr := filepath.EvalSymlinks(top); evalErr == nil {
		root = real
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return dir
	}
	for d := abs; ; d = filepath.Dir(d) {
		if fi, serr := os.Stat(filepath.Join(d, loreDirName)); serr == nil && fi.IsDir() {
			return d
		}
		if d == root || filepath.Dir(d) == d {
			return dir
		}
	}
}

// workflowNoun names the workflow in a notice when its path is unknown.
const workflowNoun = "lore-sync-merge workflow"

// DisplayPath is path as a notice shows it (v0.1.17 sync_wire.go
// displayPath): slash separated and relative to root when it is inside it,
// as given otherwise, and the workflow's name when path is empty.
func DisplayPath(root, path string) string {
	if path == "" {
		return workflowNoun
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}
