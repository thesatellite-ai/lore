package sqlite

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestFreeBytesAndDiskFull (E-FATES-3, port of v0.1.17 guard_test.go
// TestFreeBytesAndDiskFull): the probe answers for a real folder, walks up
// to an existing ancestor for a path not created yet, and a temp folder is
// not reported full. On an OS without a probe it answers -1 (unknown),
// which is never reported as full.
func TestFreeBytesAndDiskFull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got := FreeBytes(dir)
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		if got <= 0 {
			t.Fatalf("free bytes of a temp dir = %d, want > 0", got)
		}
	}
	if got == 0 {
		t.Fatal("a temp dir with zero free bytes is implausible")
	}
	if DiskFull(dir) {
		t.Fatal("temp dir reported full")
	}
	if deep := FreeBytes(filepath.Join(dir, "not", "yet", "created")); deep < 0 && got >= 0 {
		t.Fatal("probe must walk up to an existing ancestor")
	}
}

// TestExplainOpenFailure (E-FATES-3, v0.1.17 explainStorageError CH-7):
// SQLite's CANTOPEN on a folder with less than lowSpaceBytes free is
// E_DISK_FULL (a full disk cannot create the WAL and -shm files, so the
// open fails before any write); with room, or unknown free space, it stays
// what it was. SQLITE_FULL is E_DISK_FULL whatever the free space says; an
// error with its own lore code keeps it.
func TestExplainOpenFailure(t *testing.T) {
	t.Parallel()
	cantOpen := errs.Wrap(errs.Internal, "sqlite cache: read layout version", errors.New("unable to open database file: out of memory (14)"))
	full := errors.New("database or disk is full (13)")
	for _, tc := range []struct {
		name string
		err  error
		free int64
		want errs.Code // "" = unchanged
	}{
		{"cantopen, disk nearly full", cantOpen, lowSpaceBytes - 1, errs.DiskFull},
		{"cantopen, disk empty", cantOpen, 0, errs.DiskFull},
		{"cantopen, room left", cantOpen, lowSpaceBytes, ""},
		{"cantopen, free space unknown", cantOpen, -1, ""},
		{"sqlite full, room left", full, 1 << 30, errs.DiskFull},
		{"other error, disk full", errors.New("no such table: x"), 0, ""},
		{"own code wins", errs.Wrap(errs.ReadOnlyFS, "read-only", cantOpen), 0, ""},
	} {
		probed := ""
		got := explainOpen(filepath.Join(t.TempDir(), "missing", "c.db"), tc.err, func(p string) int64 { probed = p; return tc.free })
		if tc.want == "" {
			if !errors.Is(got, tc.err) || got.Error() != tc.err.Error() {
				t.Errorf("%s: changed to %v", tc.name, got)
			}
			continue
		}
		var e *errs.Error
		if !errors.As(got, &e) || e.Code != tc.want || e.Hint == "" || errs.DefOf(e.Code).Exit != 23 {
			t.Errorf("%s: %v, want %s exit 23 with a hint", tc.name, got, tc.want)
		}
		if !errors.Is(got, tc.err) {
			t.Errorf("%s: the cause is lost", tc.name)
		}
		if tc.free < lowSpaceBytes && probed == "" {
			t.Errorf("%s: free space never probed", tc.name)
		}
	}
	if explainOpen("x.db", nil, func(string) int64 { return 0 }) != nil {
		t.Fatal("nil stays nil")
	}
}

// TestOpenOnAFullDiskIsDiskFull (E-FATES-3): opening a cache file SQLite
// cannot create answers E_DISK_FULL when the probe says the disk is full,
// before anything is written, and the plain error when there is room.
func TestOpenOnAFullDiskIsDiskFull(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(f.dir, "no-such-folder", "cache.db") // SQLite answers CANTOPEN
	_, err := openCache(f.ctx, path, f.reg, f.exp, func(string) int64 { return 0 })
	if !errs.Has(err, errs.DiskFull) {
		t.Fatalf("full disk: %v, want E_DISK_FULL", err)
	}
	_, err = openCache(f.ctx, path, f.reg, f.exp, func(string) int64 { return 1 << 30 })
	if err == nil || errs.Has(err, errs.DiskFull) {
		t.Fatalf("room left: %v, want the open error, not E_DISK_FULL", err)
	}
	if fileExists(path) {
		t.Fatal("a failed open wrote the cache file")
	}
}
