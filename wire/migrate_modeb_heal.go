package wire

// migrate_modeb_heal.go — a repo an older lore's shared-database
// conversion left with no project (devco D-855).
//
// That conversion wrote the project's rows into the root folder of the
// converted folder store (<store>/_store/, store-wide, no project), never
// the project row itself, and attached the store whole as "shared". The
// repo then had no project: every plain add and render failed E_NOT_FOUND
// "this repository has no lore project". lore fix (and lore migrate run)
// repairs that shape:
//
//	.lore/lore.yaml.migrated + stores.yaml "shared" at the store's root,
//	the store being <db dir>/stores/<name> (a lore-store.yaml)
//	  ─▶ the root's row files move into the folder modeBFolder(<name>),
//	     whose _meta.json is given the project's id
//	  ─▶ the project row (and any row the root lacks) is written from the
//	     shared lore.db, which the conversion never changed
//	  ─▶ the stores.yaml entry is attached at that folder
//
// A row the folder already holds stays in the root as it is (never
// overwritten). A repo whose entry already names a folder is left alone.

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// modeBHealPlan is what healModeB would do in one repo.
type modeBHealPlan struct {
	pointer    string // the kept .lore/lore.yaml.migrated (or .toml)
	pid        ids.ID[ids.Project]
	dir        string   // the converted folder store on disk
	folderName string   // its project folder
	moves      []string // row files of the root, slash paths under the data folder ("rules/rul_….json")
}

// modeBHealNeeded is the heal plan of the repo at top; ok is false when
// the repo is not in the shape an older conversion left.
func modeBHealNeeded(top string) (modeBHealPlan, bool) {
	pointer, ok := convertedModeBPointer(top)
	if !ok {
		return modeBHealPlan{}, false
	}
	pidText, ok := convertedModeBProjectID(top)
	if !ok {
		return modeBHealPlan{}, false
	}
	pid, err := ids.Parse[ids.Project](pidText)
	if err != nil {
		return modeBHealPlan{}, false
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return modeBHealPlan{}, false
	}
	i := slices.IndexFunc(cfg.Stores, func(s attach.Store) bool { return string(s.Name) == ModeBStoreName })
	if i < 0 || cfg.Stores[i].Folder != "" {
		return modeBHealPlan{}, false
	}
	dir := fileuri.LocalPath(cfg.Stores[i].URL)
	if dir == "" || filepath.Base(filepath.Dir(dir)) != modeBStoresDir {
		return modeBHealPlan{}, false // not a store this conversion made
	}
	for _, s := range cfg.Stores {
		if s.Folder != "" && s.URL == cfg.Stores[i].URL {
			// the person attached a folder of it themselves (store folder
			// add, move --to shared/<folder>): that folder is the repo's
			// project (convertedModeBHome), nothing is moved
			return modeBHealPlan{}, false
		}
	}
	if _, merr := rowfs.ReadMarker(dir); merr != nil {
		return modeBHealPlan{}, false
	}
	plan := modeBHealPlan{pointer: pointer, pid: pid, dir: dir, folderName: modeBFolder(filepath.Base(dir))}
	plan.moves = modeBRootRowFiles(dir)
	return plan, true
}

// convertedModeBPointer is the kept pointer of a converted repo.
func convertedModeBPointer(top string) (string, bool) {
	for _, n := range modeBPointerNames {
		p := filepath.Join(top, modeBDir, n+ModeBMigratedSuffix)
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// modeBRootRowFiles lists the row files in the store's root folder
// (<dir>/_store/<table>/<id>.json), sorted.
func modeBRootRowFiles(dir string) []string {
	base := filepath.Join(dir, filepath.FromSlash(rowfs.LayoutStore.DataPath(rowfs.FolderRoot)))
	tables, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, t := range tables {
		if !t.IsDir() || !rowfs.IsTableDir(t.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(base, t.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			rel := t.Name() + "/" + f.Name()
			if _, _, ok := rowfs.ParseRowPath(rel); ok && f.Type().IsRegular() {
				out = append(out, rel)
			}
		}
	}
	slices.Sort(out)
	return out
}

// healModeB repairs a repo an older conversion left with no project (the
// file comment); it returns one step line per row and change, nothing for
// any other repo. Under dryRun it changes nothing.
func (a *App) healModeB(ctx context.Context, top string, dryRun bool) ([]string, int, error) {
	plan, ok := modeBHealNeeded(top)
	if !ok {
		return nil, 0, nil
	}
	raw := fileuri.URL(plan.dir, "")
	dataDir := filepath.Join(plan.dir, filepath.FromSlash(rowfs.LayoutStore.DataPath(plan.folderName)))
	held := map[string]bool{}
	steps := make([]string, 0, len(plan.moves)+3)
	for _, rel := range plan.moves {
		if _, err := os.Lstat(filepath.Join(dataDir, filepath.FromSlash(rel))); err == nil {
			continue // the folder has its own copy: the root's stays as it is
		}
		held[strings.TrimSuffix(rel, rowfs.RowFileExt)] = true
		steps = append(steps, modeBStepPrefix+strings.TrimSuffix(rel, rowfs.RowFileExt)+" → "+modeBPlace(plan.folderName)+" (moved out of the store's root folder, which belongs to no project)")
	}
	var changes []store.Change
	if a.opts.ModeB != nil {
		exp, err := a.opts.ModeB.ExportModeB(ctx, plan.pointer)
		if err != nil {
			return nil, 0, errs.Prefix("migrate: the project row of the converted shared database", err).
				WithHint("the shared lore.db named by " + plan.pointer + " holds the project row; put it back at that path, then run `lore fix` again")
		}
		all, err := modeBChanges(ctx, exp)
		if err != nil {
			return nil, 0, err
		}
		for _, c := range all {
			k := c.Row.Table.String() + "/" + c.Row.ID.String()
			if held[k] {
				continue
			}
			if _, err := os.Lstat(filepath.Join(dataDir, filepath.FromSlash(k+rowfs.RowFileExt))); err == nil {
				continue
			}
			changes = append(changes, c)
			steps = append(steps, modeBStepPrefix+k+" → "+modeBPlace(plan.folderName)+" (from the shared lore.db)")
		}
	}
	steps = append(steps, modeBStepPrefix+attach.FileName+": store "+ModeBStoreName+" attached at its folder "+plan.folderName+" (the repo's project)")
	moved := len(held)
	if dryRun {
		return steps, moved + len(changes), nil
	}
	if err := a.markModeBWriting(ctx); err != nil {
		return nil, 0, err
	}
	if err := seedModeBFolder(ctx, plan.dir, plan.folderName, plan.pid); err != nil {
		return nil, 0, err
	}
	if err := moveModeBRootRows(ctx, plan.dir, dataDir, held); err != nil {
		return nil, 0, err
	}
	written, err := a.writeModeBStore(ctx, plan.dir, raw, plan.folderName, plan.pid, changes)
	if err != nil {
		return nil, 0, err
	}
	if err := setStoresEntryFolder(top, store.Source(ModeBStoreName), plan.folderName); err != nil {
		return nil, 0, err
	}
	return steps, moved + written, nil
}

// moveModeBRootRows renames the root row files named in move (table/id,
// no extension) into dataDir, under the store's lock.
func moveModeBRootRows(ctx context.Context, dir, dataDir string, move map[string]bool) error {
	if len(move) == 0 {
		return nil
	}
	root, err := rowfs.Open(dir)
	if err != nil {
		return errs.Prefix("migrate: open the shared store", err)
	}
	lock, err := root.Lock(ctx)
	if err != nil {
		return errs.Prefix("migrate: lock the shared store", err)
	}
	base := filepath.Join(dir, filepath.FromSlash(rowfs.LayoutStore.DataPath(rowfs.FolderRoot)))
	var moveErr error
	for _, k := range slices.Sorted(maps.Keys(move)) {
		rel := filepath.FromSlash(k + rowfs.RowFileExt)
		dst := filepath.Join(dataDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			moveErr = errs.FileIO("create the table folder", filepath.Dir(dst), err)
			break
		}
		if err := os.Rename(filepath.Join(base, rel), dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
			moveErr = errs.FileIO("move a row file into the project folder", dst, err)
			break
		}
	}
	return errors.Join(moveErr, lock.Release())
}
