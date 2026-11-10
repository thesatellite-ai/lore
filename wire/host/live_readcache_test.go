package host

// live_readcache_test.go — a live session runs git through one
// gitref.ReadCache for its operation (live_hook.go liveRun.session), so the
// per-row reads of refs/lore/live, HEAD and origin/<default> run git once,
// and prepare drops every cached answer once the live lock is held, because
// another lore process may have moved refs while this one waited for it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// TestLiveSessionLockDropsCachedReads: a ref moved by another process is
// not seen from the session's cache (one operation reads one view), and is
// seen as soon as prepare has taken the live lock.
func TestLiveSessionLockDropsCachedReads(t *testing.T) {
	f := newLiveFixture(t)
	if f.s.cache == nil {
		t.Fatal("a live session must run git through its read cache")
	}
	const ref = "refs/heads/main"
	before, ok, err := f.s.repo.ResolveRef(f.ctx, ref)
	if err != nil || !ok {
		t.Fatalf("ResolveRef: %v %v", ok, err)
	}
	gitT(t, f.b.top, "commit", "-q", "--allow-empty", "-m", "another process")
	if got, _, _ := f.s.repo.ResolveRef(f.ctx, ref); got != before {
		t.Fatalf("inside one operation the cached answer stands: got %s want %s", got, before)
	}
	// The next prepare (a run that takes the lock again).
	f.s.release()
	f.s.lock, f.s.prepared = nil, false
	f.s.prepare(f.ctx)
	if f.s.lock == nil {
		t.Fatal("prepare did not take the live lock")
	}
	after, _, err := f.s.repo.ResolveRef(f.ctx, ref)
	if err != nil || after == before {
		t.Fatalf("after the lock the moved ref must be read again: got %s (before %s) %v", after, before, err)
	}
}

// TestLiveSessionSeesRefsADelegateMoved: `lore sync promote` commits onto a
// branch through reposync, which runs its own git (not the session's
// cached runner); the next read of that branch in the same operation must
// see the new commit, not the session's cached answer from before.
func TestLiveSessionSeesRefsADelegateMoved(t *testing.T) {
	f := newLiveFixture(t)
	dir := f.b.top
	gitT(t, dir, "config", "user.name", "Promoter")
	gitT(t, dir, "config", "user.email", "promoter@example.com")
	gitT(t, dir, "branch", "side")
	const id = "mem_promotecachetest"
	file := filepath.Join(dir, ".lore", "data", "memories", id+".json")
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"id":"`+id+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const ref = "refs/heads/side"
	before, ok, err := f.s.repo.ResolveRef(f.ctx, ref) // now cached
	if err != nil || !ok {
		t.Fatalf("ResolveRef: %v %v", ok, err)
	}
	if _, err := (liveSync{h: f.s.h}).SyncPromote(f.ctx, opssync.PromoteIn{IDs: []string{id}, Branch: "side"}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	after, ok, err := f.s.repo.ResolveRef(f.ctx, ref)
	if err != nil || !ok || after == before {
		t.Fatalf("after promote the session must read the moved branch: got %s (before %s) %v %v", after, before, ok, err)
	}
	if _, found, err := f.s.repo.ReadFile(f.ctx, ref, ".lore/data/memories/"+id+".json"); err != nil || !found {
		t.Fatalf("the promoted file must be on the branch as the session reads it: %v %v", found, err)
	}
}

// TestLiveSessionSeesWritesBesideItsCache: the run's structural guard
// (withRun's gitref.WriteObserver). A write through any gitref.Exec under
// the run's context that does not go through the session's cache (here a
// Repo opened without it, as the signals store and reposync open theirs)
// drops the session's cached answers, so the next read in the same
// operation sees it. This is the class the atomic signals push
// (87300044) and reposync's promote (32785c48) each had to be fixed for
// one by one.
func TestLiveSessionSeesWritesBesideItsCache(t *testing.T) {
	f := newLiveFixture(t)
	const ref = "refs/heads/main"
	before, ok, err := f.s.repo.ResolveRef(f.ctx, ref) // now cached
	if err != nil || !ok {
		t.Fatalf("ResolveRef: %v %v", ok, err)
	}
	beside, err := gitref.Open(f.ctx, f.b.top, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := beside.Run(f.ctx, gitref.Invocation{Args: []string{"commit", "-q", "--allow-empty", "-m", "beside the cache"},
		Env: []string{"GIT_AUTHOR_NAME=b", "GIT_AUTHOR_EMAIL=b@example.com", "GIT_COMMITTER_NAME=b", "GIT_COMMITTER_EMAIL=b@example.com"}}); err != nil {
		t.Fatalf("commit beside the cache: %v", err)
	}
	after, ok, err := f.s.repo.ResolveRef(f.ctx, ref)
	if err != nil || !ok || after == before {
		t.Fatalf("a write beside the session's cache must be seen by its next read: got %s (before %s) %v %v", after, before, ok, err)
	}
}
