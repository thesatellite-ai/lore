package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// newProcess drops what this process holds of b's clone (its replay and
// the replay file its open chose), as a new lore process starts without.
func newProcess(b *Backend) {
	forgetAuthority(b)
	authorityFiles.Delete(filepath.Clean(b.work))
	for _, views := range []*struct {
		sync.Mutex
		byWork map[string]*authority
	}{&headViews, &rowViews} {
		views.Lock()
		delete(views.byWork, b.work)
		views.Unlock()
	}
}

// TestCloneAuthorityReadsSavedReplay (bug hunt H-274, devco:D-1012): the
// org file check and AdminAt read the replay a store open of the clone
// kept on disk even when no open of that clone ran in this process yet.
// Before, they replayed the whole history in memory on every run (lore
// doctor checks the org file before any store open): every row version
// of the store read again, 77 s and 2.1 GB at 100k commits, 21+ minutes
// at 1M. A clone a person owns keeps reading its replay from the lore
// home, and a reader never starts a replay file nothing saved.
func TestCloneAuthorityReadsSavedReplay(t *testing.T) {
	o := newOrgTeam(t)
	spoof, roster := mustID[ids.Memory](t), mustID[ids.ActorPeriod](t)
	o.h.commit(o.max.email, map[string]string{orgPath("memories", spoof): stampedRow(spoof, o.boss.actor, "2026-01-02T00:00:01.000Z", "says the admin")})
	o.h.commit(o.max.email, map[string]string{orgPath("actor_periods", roster): o.max.periodRowAt("2026-01-03T00:00:00.000Z", "admin", "")})
	home := filepath.Join(t.TempDir(), "home")
	dir := filepath.Join(home, StoresDir, "host", "store")
	ctx := context.Background()

	// a reader before any open saved a replay: in memory, nothing written
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, filepath.Dir(dir), "clone", "--quiet", o.h.remote, dir)
	h := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD"))
	before := gitFolderFiles(t, dir)
	if _, ok := OrgFileChanges(ctx, dir, h); !ok {
		t.Fatal("OrgFileChanges: no replay")
	}
	if after := gitFolderFiles(t, dir); len(after) != len(before) {
		t.Fatalf("a reader started a replay file nothing saved:\nbefore %v\nafter  %v", before, after)
	}
	authorityCache.Lock()
	delete(authorityCache.byWork, dir)
	authorityCache.Unlock()

	// a store open keeps the replay in the clone's git folder
	b, err := OpenClone(ctx, o.h.remote, dir, rowfs.FolderOrg, store.OpenOptions{Home: home}, WithMerger(newestMerger{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	want := authorityNow(t, b)
	if _, err := os.Stat(b.authorityPath()); err != nil {
		t.Fatalf("the open kept no replay: %v", err)
	}
	if heldReason(t, b, roster) == "" {
		t.Fatal("the member's own admin stint is not held: the fixture lost its point")
	}

	// a new process: the org file check and AdminAt come first
	newProcess(b)
	n := replayCount()
	changes, ok := OrgFileChanges(ctx, dir, h)
	if !ok {
		t.Fatal("OrgFileChanges: no replay")
	}
	if got := replayCount() - n; got != 0 {
		t.Errorf("the org file check replayed %d commit(s) with a replay saved at HEAD, want 0", got)
	}
	if len(changes) != len(want.orgFile) {
		t.Errorf("org file changes: got %d, want %d", len(changes), len(want.orgFile))
	}
	newProcess(b)
	n = replayCount()
	if admin, decided := AdminAt(ctx, dir, h, o.boss.email); !admin || !decided {
		t.Errorf("AdminAt(boss) = %v, %v; want an admin", admin, decided)
	}
	if got := replayCount() - n; got != 0 {
		t.Errorf("AdminAt replayed %d commit(s) with a replay saved at HEAD, want 0", got)
	}
	newProcess(b)
	_, got := cloneAuthority(ctx, dir) // the whole replay, from the saved file
	sameAuthority(t, got, want)
}

// TestSavedAuthorityFileOwnClone: a clone a person owns under the lore
// home's stores folder (recorded as their own) reads its replay from the
// lore home's cache, never from or into its git folder (devco:D-812);
// a folder under no stores folder has no saved replay to find.
func TestSavedAuthorityFileOwnClone(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	dir := filepath.Join(home, StoresDir, "host", "mine")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"HEAD", authorityFile} {
		if err := os.WriteFile(filepath.Join(dir, ".git", f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := savedAuthorityFile(dir); got != filepath.Join(dir, ".git", authorityFile) {
		t.Fatalf("lore's own clone: %q, want its git folder's replay", got)
	}
	r, err := RemoteOfRaw("git@example.com:acme/mine.git")
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordClone(home, CloneKey(r, ""), CloneEntry{Path: dir, Why: WhyOwn}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := savedAuthorityFile(dir); got != "" {
		t.Fatalf("a person's own clone with no replay in the lore home: %q, want none (never its git folder)", got)
	}
	cached := authorityFileFor(dir, home, true)
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cached, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := savedAuthorityFile(dir); got != cached {
		t.Fatalf("a person's own clone: %q, want the lore home's %q", got, cached)
	}
	if got := savedAuthorityFile(filepath.Join(t.TempDir(), "code", "notes")); got != "" {
		t.Fatalf("a folder under no stores folder: %q, want none", got)
	}
}
