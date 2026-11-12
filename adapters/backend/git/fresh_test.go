package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
)

// TestFetchStamp (RES 3.6, P2-RES-7, later 548): a repo-mode backend and a
// missing or unreadable stamp report Known false; a successful fetch
// writes .git/lore/fetch-stamp in the clone with the clock's
// time (UTC, second precision) and Fresh reports it with the worktree's
// HEAD; a failed fetch (remote gone) keeps the older stamp.
func TestFetchStamp(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenRepo(ctx, newRepo(t), rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f, ferr := repo.Fresh(ctx); ferr != nil || f.Known {
		t.Fatalf("repo mode: %+v %v", f, ferr)
	}
	_ = repo.Close()

	remote := newRemote(t, true)
	now := time.Date(2026, 11, 5, 10, 0, 0, 500, time.FixedZone("x", 3600))
	b := clone(t, remote, WithClock(func() time.Time { return now }))
	syncOK(t, b)
	f, err := b.Fresh(ctx)
	if err != nil || !f.Known || !f.FetchedAt.Equal(now.Truncate(time.Second)) || f.FetchedAt.Location() != time.UTC {
		t.Fatalf("after a fetch: %+v %v (want %v UTC)", f, err, now)
	}
	if head := gitT(t, b.work, "rev-parse", "HEAD"); f.Commit != head {
		t.Fatalf("commit %q, want HEAD %q", f.Commit, head)
	}
	stamp := filepath.Join(b.work, ".git", StateDirName, FetchStampFile)
	if _, err = os.Stat(stamp); err != nil {
		t.Fatalf("stamp not in the clone's .git/lore: %v", err)
	}
	if !FetchedOnce(b.work) {
		t.Fatal("FetchedOnce is false after a fetch")
	}

	// a failed fetch keeps the older stamp
	first := now
	now = now.Add(time.Hour)
	if err = os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}
	_, _ = b.Sync(ctx) // offline: an error or a report, never a new stamp
	if f, err = b.Fresh(ctx); err != nil || !f.FetchedAt.Equal(first.Truncate(time.Second)) {
		t.Fatalf("after a failed fetch: %+v %v", f, err)
	}

	for name, content := range map[string]string{"garbage": "yesterday\n", "empty": ""} {
		if err = os.WriteFile(stamp, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if f, err = b.Fresh(ctx); err != nil || f.Known {
			t.Fatalf("%s stamp: %+v %v", name, f, err)
		}
	}
	if err = os.Remove(stamp); err != nil {
		t.Fatal(err)
	}
	if f, err = b.Fresh(ctx); err != nil || f.Known {
		t.Fatalf("no stamp: %+v %v", f, err)
	}
}
