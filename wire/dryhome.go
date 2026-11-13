package wire

// dryhome.go — `lore fix --dry-run` writes nothing under the lore home
// (B4-14). Its run opens the project like any other, and an open builds
// caches (cache/…, rebuildable) and the machine store (state/machine.db).
// Under the dry-run switch (git.ForbidClones, set by the CLI entry point
// for that one command) those go to a scratch folder instead: the caches
// start as copies of the real ones (seedDryCache) and catch up from the
// row files, the machine store is a copy of the real one, and the CLI
// removes the folder when the command ends (RemoveDryScratch).
//
// Why the caches are copied (T-3): a scratch cache built from nothing
// read, decoded and indexed every row on each dry run (5,500 rows: 5-17 s
// for `lore fix --dry-run`); a copy is current, so its refresh reads only
// what changed since the last real run, as a normal command does.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
)

var (
	dryScratchMu  sync.Mutex
	dryScratchDir string
	// dryRealHome is the lore home the scratch folder stands in for.
	dryRealHome string
)

// dryRunProcess reports the dry-run switch (git.ForbidClones).
func dryRunProcess() bool { return git.NoClone(context.Background()) }

// writableHome is where an open writes its caches and machine store:
// home, or under the dry-run switch a scratch copy's folder (the real
// machine store copied in once, so reads still see it).
func writableHome(home string) string {
	if !dryRunProcess() || home == "" {
		return home
	}
	dryScratchMu.Lock()
	defer dryScratchMu.Unlock()
	if dryScratchDir == "" {
		dir, err := os.MkdirTemp("", "lore-dry-run-")
		if err != nil {
			return home
		}
		dryScratchDir, dryRealHome = dir, home
		src := filepath.Join(home, filepath.FromSlash(MachineStoreFile))
		if b, rerr := os.ReadFile(filepath.Clean(src)); rerr == nil {
			dst := filepath.Join(dir, filepath.FromSlash(MachineStoreFile))
			if os.MkdirAll(filepath.Dir(dst), cacheDirMode) == nil {
				_ = os.WriteFile(dst, b, 0o600) // best effort: an empty machine store reads as none
			}
		}
	}
	return dryScratchDir
}

// seedDryCache copies the real cache that path, a cache file in the
// dry run's scratch folder, stands for (same place under the real lore
// home), with its WAL, unless the scratch file exists already. True when it
// copied. Best effort: no real file, or one that changed while it was
// copied (a lore running beside this one wrote it: the copy may be torn),
// leaves no copy, and the cache is built from the row files as before.
func seedDryCache(path string) bool {
	dryScratchMu.Lock()
	scratch, home := dryScratchDir, dryRealHome
	dryScratchMu.Unlock()
	if scratch == "" || home == "" {
		return false
	}
	rel, err := filepath.Rel(scratch, path)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) > 2 && rel[:3] == ".."+string(filepath.Separator) {
		return false
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return false
	}
	src := filepath.Join(home, rel)
	files := []string{"", "-wal"}
	before := make([]os.FileInfo, len(files))
	for i, s := range files {
		fi, err := os.Stat(src + s)
		if err != nil && (i == 0 || !os.IsNotExist(err)) {
			return false
		}
		before[i] = fi
	}
	ok := true
	for i, s := range files {
		if before[i] != nil && copyFile(src+s, path+s) != nil {
			ok = false
			break
		}
	}
	for i, s := range files {
		fi, err := os.Stat(src + s)
		if !ok || !unchangedFile(before[i], fi, err) {
			ok = false
			break
		}
	}
	if !ok {
		dropDryCache(path)
	}
	return ok
}

// unchangedFile reports whether a file stat'ed again (fi, err) is unchanged
// from before (nil: it did not exist).
func unchangedFile(before, fi os.FileInfo, err error) bool {
	if before == nil {
		return os.IsNotExist(err)
	}
	return err == nil && fi.Size() == before.Size() && fi.ModTime().Equal(before.ModTime())
}

// dropDryCache removes a scratch cache copy and its sidecars.
func dropDryCache(path string) {
	for _, s := range append([]string{""}, cacheSidecars...) {
		_ = os.Remove(path + s)
	}
}

// copyFile copies src to a new file dst (0600).
func copyFile(src, dst string) (err error) {
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	if err := os.MkdirAll(filepath.Dir(dst), cacheDirMode); err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Clean(dst), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

// RemoveDryScratch removes the scratch folder; the CLI calls it when the
// dry-run command ends (several Apps of one process may share it).
func RemoveDryScratch() {
	dryScratchMu.Lock()
	defer dryScratchMu.Unlock()
	if dryScratchDir != "" {
		_ = os.RemoveAll(dryScratchDir)
		dryScratchDir, dryRealHome = "", ""
	}
}
