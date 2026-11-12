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
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
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

// DoctorCaches integrity-checks every cache file lore keeps: every *.db
// under <home>/cache (the repo caches included, cache/repo). A damaged file is an error (doctor broken) whose fix is to
// delete it: a cache is rebuilt from the row files (spec 3). The core also
// moves a file aside by itself when it cannot open it (cache.go), but one
// damaged past its first pages opens fine and only quick_check sees it.
// It never opens the App's state, so it runs even when the open fails.
func (a *App) DoctorCaches(ctx context.Context) ([]DoctorFinding, error) {
	var files []string // the repo's own cache is under <home>/cache/repo (RepoCachePath)
	if a.opts.Home != "" {
		_ = filepath.WalkDir(filepath.Join(a.opts.Home, cacheDir), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, cacheExt) {
				files = append(files, p)
			}
			return nil // an unreadable folder is skipped, never fatal
		})
	}
	var out []DoctorFinding
	for _, f := range files {
		if msg := cacheFinding(ctx, f); msg != "" {
			out = append(out, DoctorFinding{Check: DoctorCheckCache, Level: DoctorError, Plain: true, Message: msg})
		}
	}
	return out, nil
}

// cacheFinding returns the doctor line for one cache file, "" when it is
// sound or absent. Damage (IsCorrupt, or quick_check not ok) names the
// fix; a lock or permission problem is reported as it is, without the
// delete advice, because the file may be fine. Every line is an error
// (doctor broken), as v0.1.17 counted it.
func cacheFinding(ctx context.Context, p string) string {
	if _, err := os.Stat(p); err != nil {
		return "" // absent: the next command creates it
	}
	fix := "cache " + p + " is damaged; delete it (and its -wal/-shm files): the next lore command rebuilds it from .lore/data, nothing is lost"
	res, err := sqlite.QuickCheck(ctx, p)
	switch {
	case err != nil && sqlite.IsCorrupt(err):
		return fix + " (" + err.Error() + ")"
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
		Message: EnvRemovedSyncAllow + " is set but does nothing since 0.1.18 (the hooks always scan); unset it, and switch one rule off with `lore secrets rule off <id> --reason …` instead"}}, nil
}
