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

// NoAccessSkips reports whether a read skips the clone at dir as one this
// login cannot reach: its last fetch was refused (NoAccessRecorded) and
// it holds no copy of the store (no fetch ever worked here: no fetch
// stamp). Every reader that skips a refused store asks this one rule
// (devco D-741).
//
// Why the local copy decides: a record is written by whichever lore run
// saw the refusal, and that run may have had other credentials than the
// person's own (a pre-commit hook run by an editor with no ssh agent, a
// harness git with another HOME). A store this machine has a copy of is
// read from that copy, as any offline store is, so one refused fetch
// never hides a store the person reads and writes every day; the next
// fetch that works removes the record (stampFetch, the org reader's
// fetchLocked). A store never fetched here has nothing to read, so it is
// skipped without a new connection until `lore sync` tries it again.
func NoAccessSkips(dir string) bool {
	return NoAccessRecorded(dir) && !FetchedOnce(dir)
}

// ErrNoAccessRecorded marks a refused first open whose cause is the
// clone's no-access record (the fetch's own text may say less, e.g. a
// fetch that was skipped), so AccessRefused answers true for it.
var ErrNoAccessRecorded = errors.New("git: the last fetch of this store was refused (no access, or it is gone)")
