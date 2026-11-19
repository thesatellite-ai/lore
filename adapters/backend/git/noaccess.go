package git

// noaccess.go — the no-access record of a store clone (devco D-726).
//
// The org link reader (adapters/orgcfg) fetches every org store a person
// may read. When the forge refuses that fetch (no access, or the store is
// gone) it writes <clone>/.git/lore/no-access, so the store is skipped
// for that person instead of read as offline. The record lives here,
// beside the clone layout, because the backend's own open of the same
// clone must read it too: a write to a store this login cannot reach
// used to refuse with "no local copy yet and cannot be reached / go
// online once", which sends the person to fix a network that works.
//
//	open of a never-cloned store ─▶ fetch fails, nothing local
//	  remote refused (accessRefused) or no-access record ─▶ msgAccessRefused
//	  anything else                                      ─▶ "go online once"

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// NoAccessFile is the record's file name in a clone's state dir
// (StateDirOf): "<gitfault kind> <RFC 3339 time>".
const NoAccessFile = "no-access"

// NoAccessPath is the no-access record of the clone at dir.
func NoAccessPath(dir string) string {
	return filepath.Join(StateDirOf(dir), NoAccessFile)
}

// NoAccessRecorded reports whether the last fetch of the clone at dir was
// refused (the record exists and names a kind).
func NoAccessRecorded(dir string) bool {
	b, err := os.ReadFile(filepath.Clean(NoAccessPath(dir)))
	return err == nil && strings.TrimSpace(string(b)) != ""
}

// ErrNoAccessRecorded marks a refused first open whose cause is the
// clone's no-access record (the fetch's own text may say less, e.g. a
// fetch that was skipped), so AccessRefused answers true for it.
var ErrNoAccessRecorded = errors.New("git: the last fetch of this store was refused (no access, or it is gone)")
