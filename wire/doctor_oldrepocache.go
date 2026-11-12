package wire

// doctor_oldrepocache.go — the repo cache an earlier build kept inside the
// work tree (.lore/lore-cache.db, RepoCacheFile). The repo store's cache
// now lives under the lore home (RepoCachePath, owner item 16), so a read
// creates nothing in the repo. lore never deletes the old file by itself:
// `lore doctor` names it as safe to delete (it is untracked and rebuilt
// from the row files) and `lore doctor --fix` / `lore fix` remove it with
// its WAL sidecars.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// DoctorCheckOldRepoCache names the doctor check.
const DoctorCheckOldRepoCache DoctorCheck = "old_repo_cache"

// oldRepoCacheFiles are the old in-tree cache and its sidecars under top
// that exist.
func oldRepoCacheFiles(top string) []string {
	var out []string
	for _, s := range append([]string{""}, cacheSidecars...) {
		p := filepath.Join(top, filepath.FromSlash(RepoCacheFile)+s)
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			out = append(out, p)
		}
	}
	return out
}

// DoctorOldRepoCache notes an old .lore/lore-cache.db in the repo; with
// fix it is removed (a note either way: nothing is broken).
func (a *App) DoctorOldRepoCache(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	top, err := a.repoTop(ctx)
	if err != nil || top == "" {
		return nil, nil // no repo: nothing in a work tree to name
	}
	files := oldRepoCacheFiles(top)
	if len(files) == 0 {
		return nil, nil
	}
	old := files[0]
	f := DoctorFinding{Check: DoctorCheckOldRepoCache, Level: DoctorNote,
		Message: old + " is the repo cache of an earlier lore build (the cache now lives under " + filepath.Join(a.opts.Home, cacheDir, repoCacheDir) + "); it is untracked and rebuildable, so it is safe to delete",
		Fix:     "lore fix (or rm " + old + "*)"}
	if fix {
		var all []error
		for _, p := range files {
			if rerr := os.Remove(p); rerr != nil && !os.IsNotExist(rerr) {
				all = append(all, rerr)
			}
		}
		if len(all) > 0 {
			return []DoctorFinding{f}, errors.Join(all...)
		}
		f.Fixed = true
	}
	return []DoctorFinding{f}, nil
}
