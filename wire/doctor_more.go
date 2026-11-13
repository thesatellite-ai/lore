package wire

// doctor_more.go — three `lore doctor` checks moved from the old binary
// (saas/cmd/cli doctor_cache.go, doctor_stores.go, doctor_secrets.go) into
// the core so every binary runs them through ops/doctor (B11). Their lines
// are the v0.1.17 text word for word (DoctorFinding.Plain):
//
//	DoctorResurrected  a live row file of a purged id an old clone brought back (DQ4, DELETION 2.7): warning
//	DoctorCaches       a cache file that fails PRAGMA quick_check (spec 3: rebuildable, so the fix is delete): error
//	DoctorRemovedEnv   LORE_SYNC_ALLOW_SECRETS still set though 0.1.18 removed it (D3): warning

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	"github.com/thesatellite-ai/lore/store"
)

// The checks of this file (the JSON "check" names).
const (
	DoctorCheckResurrected DoctorCheck = "resurrected_row"
	DoctorCheckCache       DoctorCheck = "cache"
	DoctorCheckRemovedEnv  DoctorCheck = "removed_env"
)

// EnvRemovedSyncAllow is the v0.1.17 escape hatch removed in 0.1.18 (D3):
// the export and the hooks always scan, so setting it does nothing. A
// person relying on it believes secrets pass while every hook refuses
// them, so doctor warns while it is set.
const EnvRemovedSyncAllow = "LORE_SYNC_ALLOW_SECRETS"

// rowFileRel is the path of a file of src's data folder (rel: "tasks/x.json"),
// relative to the store's top: the backend's own data path when it has one
// (a store folder: "<folder>/tasks/x.json"; a folder not moved yet keeps
// its .lore/data), else by the layout of src (a code repo's .lore/data, a
// store's folder).
func rowFileRel(b store.Backend, src store.Source, folder, rel string) string {
	if dp, ok := concrete(b).(interface{ DataPath() string }); ok {
		return path.Join(dp.DataPath(), rel)
	}
	if src == store.SourceRepo {
		return rowfs.LayoutRepo.StorePath(rowfs.FolderRoot, rel)
	}
	return rowfs.LayoutStore.StorePath(folder, rel)
}

// DoctorResurrected names every live row file of a purged row an old clone
// brought back. The engine already hides such a file from every read;
// doctor says where it is, once per file, so a person deletes it.
func (a *App) DoctorResurrected(ctx context.Context) ([]DoctorFinding, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, err
	}
	res, err := eng.Resurrected(ctx)
	if err != nil || len(res) == 0 {
		return nil, err
	}
	folders := map[store.Source]string{}
	if idents, ierr := a.StoreIdentities(ctx); ierr == nil { // no identities: paths from the store root
		for _, si := range idents {
			folders[si.Name] = si.Folder
		}
	}
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DoctorFinding, 0, len(res))
	for _, x := range res {
		p := rowFileRel(st.byName[x.Store], x.Store, folders[x.Store], rowfs.RowPath(x.Table, x.ID))
		out = append(out, DoctorFinding{Check: DoctorCheckResurrected, Level: DoctorWarning, Store: x.Store, Plain: true,
			Message: "store " + string(x.Store) + ": " + p + " is a live copy of a purged row (an old clone brought it back); lore hides it from every read; remove the file (git rm it and commit)"})
	}
	return out, nil
}

// DoctorCaches integrity-checks the cache files of this run. A damaged
// file is an error (doctor broken) whose fix is to delete it: a cache is
// rebuilt from the row files (spec 3). The core also moves a file aside by
// itself when it cannot open it (cache.go), but one damaged past its first
// pages opens fine and only quick_check sees it. It never opens the App's
// state, so it runs even when the open fails.
//
// Which files (owner item 35 follow-up): inside a repo, only this repo's
// own caches (repoCacheFiles: its repo cache, the caches of the stores it
// opened, its combined cache), never another repo's: `lore fix --all` runs
// doctor in every repo, eight at a time, and each child re-checking all
// 85+ caches waited on every sibling's busy file. The machine-wide sweep
// of every *.db under <home>/cache (DoctorAllCaches) runs once: outside
// any repo, in `lore doctor --all` (opsdoctor.MachineSweep), and in the
// --all parent of lore fix (FixCacheSweep).
func (a *App) DoctorCaches(ctx context.Context) ([]DoctorFinding, error) {
	if opsdoctor.MachineSweep(ctx) {
		return a.DoctorAllCaches(ctx)
	}
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return a.DoctorAllCaches(ctx)
	}
	return cacheFindings(ctx, a.repoCacheFiles(top)), nil
}

// DoctorAllCaches integrity-checks every cache file lore keeps: every *.db
// under <home>/cache (the repo caches of every repo included, cache/repo).
func (a *App) DoctorAllCaches(ctx context.Context) ([]DoctorFinding, error) {
	var files []string
	if a.opts.Home != "" {
		_ = filepath.WalkDir(filepath.Join(a.opts.Home, cacheDir), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, cacheExt) {
				files = append(files, p)
			}
			return nil // an unreadable folder is skipped, never fatal
		})
	}
	return cacheFindings(ctx, files), nil
}

// FixCacheSweep is the machine-wide cache sweep of `lore fix --all`, run
// once by the parent after every repo's own run (opsfix.Env): the lines of
// DoctorAllCaches.
func (a *App) FixCacheSweep(ctx context.Context) []string {
	found, _ := a.DoctorAllCaches(ctx) // never fails: an unreadable folder is skipped
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.Line())
	}
	return out
}

// repoCacheFiles are the cache files of the repo at top, sorted, each
// once: the ones its open used (repo store and every attached store, the
// combined file) when the App is open, and the paths its open would use
// for the repo store either way (RepoCachePath, the remote-linked and the
// combined file of the top), so a failed open still has its own checked.
func (a *App) repoCacheFiles(top string) []string {
	set := map[string]bool{}
	a.mu.Lock()
	if a.opened && a.st != nil {
		if a.st.repoTop != "" {
			top = a.st.repoTop
		}
		if c := a.st.caches; c != nil {
			c.mu.Lock()
			for _, p := range c.paths {
				set[p] = true
			}
			c.mu.Unlock()
		}
	}
	a.mu.Unlock()
	if a.opts.Home != "" {
		set[RepoCachePath(a.opts.Home, top)] = true
		set[hashedCacheFile(a.opts.Home, remoteRepoDir, top)] = true
		set[hashedCacheFile(a.opts.Home, combinedDir, top)] = true
	}
	files := make([]string, 0, len(set))
	for p := range set {
		if p != "" {
			files = append(files, p)
		}
	}
	slices.Sort(files)
	return files
}

// cacheFindings is cacheFinding over files, one error per bad file.
func cacheFindings(ctx context.Context, files []string) []DoctorFinding {
	var out []DoctorFinding
	for _, f := range files {
		if msg := cacheFinding(ctx, f); msg != "" {
			out = append(out, DoctorFinding{Check: DoctorCheckCache, Level: DoctorError, Plain: true, Message: msg})
		}
	}
	return out
}

// cacheFinding returns the doctor line for one cache file, "" when it is
// sound or absent. Damage (IsCorrupt, or quick_check not ok) names the
// fix; a permission or read problem is reported as it is, without the
// delete advice, because the file may be fine. Every line is an error
// (doctor broken), as v0.1.17 counted it.
//
// A file another lore process holds is not a finding (owner item 35):
// QuickCheck waits for it, and one still busy after that wait is in use
// by a live command (a sibling `lore fix --all` child writing its own
// repo's cache), which says nothing about damage; the next doctor checks
// it. Only a file doctor truly cannot read is reported.
func cacheFinding(ctx context.Context, p string) string {
	if _, err := os.Stat(p); err != nil {
		return "" // absent: the next command creates it
	}
	fix := "cache " + p + " is damaged; delete it (and its -wal/-shm files): the next lore command rebuilds it from .lore/data, nothing is lost"
	res, err := sqlite.QuickCheck(ctx, p)
	switch {
	case err != nil && sqlite.IsCorrupt(err):
		return fix + " (" + err.Error() + ")"
	case err != nil && sqlite.IsBusy(err):
		return "" // in use by a live lore command, not damaged
	case err != nil:
		return "cache " + p + ": cannot check: " + err.Error()
	case res != sqlite.QuickCheckOK:
		return fix + " (quick_check: " + res + ")"
	}
	return ""
}

// DoctorRemovedEnv warns while the removed LORE_SYNC_ALLOW_SECRETS is set
// (D3); the rules switched off are listed by `lore secrets rules`.
func (a *App) DoctorRemovedEnv(context.Context) ([]DoctorFinding, error) {
	if os.Getenv(EnvRemovedSyncAllow) == "" {
		return nil, nil
	}
	return []DoctorFinding{{Check: DoctorCheckRemovedEnv, Level: DoctorWarning, Plain: true,
		Message: EnvRemovedSyncAllow + " is set but does nothing since 0.1.18 (the hooks always scan); unset it, and switch one rule off with `lore secrets rule off --id <id> --reason …` instead"}}, nil
}
