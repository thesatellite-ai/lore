package wire

// dryhome.go — `lore fix --dry-run` writes nothing under the lore home
// (B4-14). Its run opens the project like any other, and an open builds
// caches (cache/…, rebuildable) and the machine store (state/machine.db).
// Under the dry-run switch (git.ForbidClones, set by the CLI entry point
// for that one command) those go to a scratch folder instead: the caches
// are built there from the row files, the machine store is a copy of the
// real one, and the CLI removes the folder when the command ends
// (RemoveDryScratch).

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
)

var (
	dryScratchMu  sync.Mutex
	dryScratchDir string
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
		dryScratchDir = dir
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

// RemoveDryScratch removes the scratch folder; the CLI calls it when the
// dry-run command ends (several Apps of one process may share it).
func RemoveDryScratch() {
	dryScratchMu.Lock()
	defer dryScratchMu.Unlock()
	if dryScratchDir != "" {
		_ = os.RemoveAll(dryScratchDir)
		dryScratchDir = ""
	}
}
