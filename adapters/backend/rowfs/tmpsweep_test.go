package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// A writer killed mid-write (an agent session stopped) leaves a tmp file
// in the committed table folder. The next locked write removes it however
// young it is, and the scan's age rule reads the file's real mtime against
// the wall clock, not the injected clock (LORE_NOW), under which a fresh
// file looked minus-years old and stayed forever.
func TestLockedWriteSweepsDeadWritersTmp(t *testing.T) {
	t.Parallel()
	for _, l := range []Layout{LayoutStore, LayoutRepo} {
		t.Run(l.String(), func(t *testing.T) {
			t.Parallel()
			past := func() time.Time { return time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC) }
			r, g := openRoot(t, WithLayout(l), WithClock(past)), newGen(t)
			f := folder(t, r, FolderRoot)
			apply(t, f, store.Change{Row: g.row(newID(t), "a", hlc.HLC{})})
			table := filepath.Join(f.dataDir, string(memories))
			dead := []string{filepath.Join(table, tmpPrefix+"dead0001"), filepath.Join(f.dataDir, tmpPrefix+"dead0002")}
			for _, p := range dead {
				if err := os.WriteFile(p, []byte("half a row"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			lock, err := r.Lock(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			apply(t, f, store.Change{Row: g.row(newID(t), "b", hlc.HLC{})})
			if err := lock.Release(); err != nil {
				t.Fatal(err)
			}
			for _, p := range dead {
				if _, err := os.Lstat(p); !os.IsNotExist(err) {
					t.Errorf("%s survived a locked write: %v", filepath.Base(p), err)
				}
			}
			rows, _, err := f.Read(context.Background(), "")
			if err != nil || len(rows) != 2 {
				t.Fatalf("read = %d rows, %v; want 2", len(rows), err)
			}
		})
	}
}

// A sweep never pulls a tmp file this process is still writing (a lock
// held reentrantly can run two writers of one process at once).
func TestSweepKeepsInflightTmp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	live, dead := filepath.Join(dir, tmpPrefix+"live"), filepath.Join(dir, tmpPrefix+"dead")
	for _, p := range []string{live, dead} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inflight.Store(live, struct{}{})
	defer inflight.Delete(live)
	sweepTmp(dir)
	if _, err := os.Lstat(live); err != nil {
		t.Errorf("the in-flight tmp file was swept: %v", err)
	}
	if _, err := os.Lstat(dead); !os.IsNotExist(err) {
		t.Errorf("the dead tmp file stayed: %v", err)
	}
}

// The scan (no lock) keeps a young tmp file and removes one over
// staleTmpAge by real mtime, whatever the injected clock says.
func TestScanTmpAgeUsesWallClock(t *testing.T) {
	t.Parallel()
	past := func() time.Time { return time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC) }
	r, g := openRoot(t, WithClock(past)), newGen(t)
	f := folder(t, r, FolderRoot)
	apply(t, f, store.Change{Row: g.row(newID(t), "a", hlc.HLC{})})
	table := filepath.Join(f.dataDir, string(memories))
	young, old := filepath.Join(table, tmpPrefix+"young"), filepath.Join(table, tmpPrefix+"old")
	for _, p := range []string{young, old} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	then := time.Now().Add(-2 * staleTmpAge)
	if err := os.Chtimes(old, then, then); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.Read(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(young); err != nil {
		t.Errorf("the scan removed a young tmp file: %v", err)
	}
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Errorf("the scan kept a tmp file %v old: %v", 2*staleTmpAge, err)
	}
}
