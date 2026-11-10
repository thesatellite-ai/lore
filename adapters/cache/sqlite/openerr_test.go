package sqlite

// openerr_test.go — a cache or machine-store file SQLite cannot open is
// reported with the file, SQLite's own error and a next step, never only
// "sqlite cache: open" (machines sweep, 2026-11-10).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// notADatabase is a file SQLite refuses at its first read: 4 KiB of text
// where its header should be.
func notADatabase(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Repeat("not a database ", 300)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOpenErrorNamesTheCause: opening a cache file that is not a database
// fails with a message naming the file and SQLite's error, and a hint to
// move the file aside (the cache is rebuilt from the row files).
func TestOpenErrorNamesTheCause(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "cache.db")
	notADatabase(t, path)
	_, err := Open(f.ctx, path, f.reg, f.exp)
	if err == nil {
		t.Fatal("Open of a file that is not a database succeeded")
	}
	env := errs.EnvelopeOf(err).Error
	if !strings.Contains(env.Message, path) || !strings.Contains(env.Message, "file is not a database") {
		t.Fatalf("message %q does not name the file and SQLite's error", env.Message)
	}
	if !strings.Contains(env.Hint, "mv "+path) {
		t.Fatalf("hint %q does not say to move the file aside", env.Hint)
	}
}

// TestOpenMachineStoreErrorNamesTheCause: the same for the machine store.
func TestOpenMachineStoreErrorNamesTheCause(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "machine.db")
	notADatabase(t, path)
	_, err := OpenMachineStore(t.Context(), path, false)
	if err == nil {
		t.Fatal("OpenMachineStore of a file that is not a database succeeded")
	}
	env := errs.EnvelopeOf(err).Error
	if !strings.Contains(env.Message, "machine store "+path) || !strings.Contains(env.Message, "file is not a database") {
		t.Fatalf("message %q does not name the file and SQLite's error", env.Message)
	}
	if env.Hint == "" {
		t.Fatal("no hint")
	}
}

// TestRoomForCountsInodes: a filesystem with free blocks and no free
// inodes has no room for a new file (a tmpfs at its inode cap), so a
// failed open there is E_DISK_FULL, not E_INTERNAL; a filesystem that
// reports no inode count is judged by blocks.
func TestRoomForCountsInodes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		blocks, files, ffree, want uint64
	}{
		{blocks: 1000, files: 100, ffree: 0, want: 0},
		{blocks: 1000, files: 100, ffree: 5, want: 1000},
		{blocks: 1000, files: 0, ffree: 0, want: 1000},
		{blocks: 0, files: 100, ffree: 5, want: 0},
	} {
		if got := roomFor(c.blocks, c.files, c.ffree); got != c.want {
			t.Errorf("roomFor(%d, %d, %d) = %d, want %d", c.blocks, c.files, c.ffree, got, c.want)
		}
	}
	if !lowOnSpace(freeOf(roomFor(1<<30, 100, 0), 4096)) {
		t.Error("a filesystem with no free inodes is not low on space")
	}
}

// TestOpenWithNoRoomNamesInodes: a cache file that cannot be created
// (forced: its folder does not exist, so SQLite answers CANTOPEN) on a
// filesystem the probe says has no room is E_DISK_FULL whose message names
// the file, the folder and inodes, and whose hint names df -i and df -h on
// that folder, never only "sqlite cache: open".
func TestOpenWithNoRoomNamesInodes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(f.dir, "no-such-folder", "cache.db")
	_, err := openCache(f.ctx, path, f.reg, f.exp, func(string) int64 { return 0 })
	env := errs.EnvelopeOf(err).Error
	if env.Code != errs.DiskFull {
		t.Fatalf("code %s, want E_DISK_FULL: %v", env.Code, err)
	}
	want := "cannot create the cache file " + path + ": no free inodes or disk space in " + f.dir
	if !strings.Contains(env.Message, want) {
		t.Fatalf("message %q does not contain %q", env.Message, want)
	}
	if !strings.Contains(env.Hint, "df -i "+f.dir) || !strings.Contains(env.Hint, "df -h "+f.dir) {
		t.Fatalf("hint %q does not name df -i and df -h on %s", env.Hint, f.dir)
	}
}
