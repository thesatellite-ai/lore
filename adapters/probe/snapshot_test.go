package probe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// countingGit puts a git wrapper first on PATH that logs each call's
// subcommand (the first argument that is not a -c option) to a file, then
// runs the real git. It returns a func that counts the logged calls of sub.
func countingGit(t *testing.T, s sandbox, realGit string) func(sub string) int {
	t.Helper()
	log := filepath.Join(s.home, "git-calls.log")
	fakeBin(t, s.bin, "git", `sub=""; skip=0
for a in "$@"; do
  if [ "$skip" = 1 ]; then skip=0; continue; fi
  case "$a" in -c) skip=1;; -*) ;; *) sub="$a"; break;; esac
done
echo "$sub" >> '`+log+`'
exec '`+realGit+`' "$@"`)
	return func(sub string) int {
		b, err := os.ReadFile(log)
		if err != nil {
			return 0
		}
		n := 0
		for l := range strings.SplitSeq(string(b), "\n") {
			if l == sub {
				n++
			}
		}
		return n
	}
}

// storeChecks runs what one Stores step runs at once: store.read,
// store.marker, org.free (RootFiles) and two store.write callers, in
// parallel under ctx.
func storeChecks(ctx context.Context, p *Prober, url string, route Route) (ReadResult, MarkerResult, RootFilesResult, [2]WriteResult) {
	var (
		wg sync.WaitGroup
		rr ReadResult
		mr MarkerResult
		rf RootFilesResult
		wr [2]WriteResult
	)
	wg.Add(5)
	go func() { defer wg.Done(); rr = p.ReadRemote(ctx, url, route) }()
	go func() { defer wg.Done(); mr = p.StoreMarker(ctx, url, route) }()
	go func() { defer wg.Done(); rf = p.RootFiles(ctx, url, route, "lore-org.yaml") }()
	go func() { defer wg.Done(); wr[0] = p.WriteRemote(ctx, url, route, "mch_a") }()
	go func() { defer wg.Done(); wr[1] = p.WriteRemote(ctx, url, route, "mch_a") }()
	wg.Wait()
	return rr, mr, rf, wr
}

// One run of the store checks with a RemoteMemo connects to the remote
// twice: one fetch (read, marker, org.free) and one dry-run push; never an
// ls-remote. Without a memo each check connects on its own (the old
// count), with the same answers.
func TestRemoteMemoOneFetchOnePush(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	s := newSandbox(t)
	repo := filepath.Join(t.TempDir(), "me.git")
	bareRepo(t, repo, rowfs.MarkerFile, "lore-org.yaml")
	count := countingGit(t, s, realGit) // after the seed: only the probes count
	p := s.prober(Options{Limiter: NewLimiter(8)})
	url := fileURL(repo)

	check := func(label string, rr ReadResult, mr MarkerResult, rf RootFilesResult, wr [2]WriteResult) {
		t.Helper()
		if rr.Class != ClassOK || rr.Empty || strings.Join(rr.Heads, ",") != "main" {
			t.Errorf("%s read: %+v", label, rr)
		}
		if mr.Class != ClassOK || !mr.Present || mr.EmptyRepo {
			t.Errorf("%s marker: %+v", label, mr)
		}
		if rf.Class != ClassOK || strings.Join(rf.Found, ",") != "lore-org.yaml" {
			t.Errorf("%s root files: %+v", label, rf)
		}
		for i, w := range wr {
			if w.Class != ClassOK {
				t.Errorf("%s write %d: %+v", label, i, w)
			}
		}
	}

	ctx, tr := WithTrace(WithRemoteMemo(ctxT(t)))
	rr, mr, rf, wr := storeChecks(ctx, p, url, Route{})
	check("memo", rr, mr, rf, wr)
	if f, pu, ls := count("fetch"), count("push"), count("ls-remote"); f != 1 || pu != 1 || ls != 0 {
		t.Errorf("with a memo: %d fetch, %d push, %d ls-remote; want 1, 1, 0", f, pu, ls)
	}
	// A later check in the same run reuses the answers too.
	if r := p.ReadRemote(ctx, url, Route{}); r.Class != ClassOK || count("fetch") != 1 {
		t.Errorf("a later read in the run fetched again: %+v, %d fetches", r, count("fetch"))
	}
	// Reused answers say so in the trace (Details panel: "shared").
	shared := 0
	for _, c := range tr.Calls() {
		if c.Kind == CallCache && strings.Contains(c.Command, "shared with another check") {
			shared++
		}
	}
	if shared != 4 { // marker, root files, the 2nd write, the later read
		t.Errorf("trace has %d shared calls, want 4: %+v", shared, tr.Calls())
	}

	// Another route is another answer: it connects again.
	if r := p.ReadRemote(ctx, url, Route{SSHKey: "/nonexistent/key"}); r.Class != ClassOK || count("fetch") != 2 {
		t.Errorf("another route reused the first route's fetch: %+v, %d fetches", r, count("fetch"))
	}

	// A new run (a new memo) probes afresh: nothing outlives a run.
	before := count("fetch")
	storeChecks(WithRemoteMemo(ctxT(t)), p, url, Route{})
	if got := count("fetch") - before; got != 1 {
		t.Errorf("a new run fetched %d times, want 1", got)
	}

	// No memo: every check connects on its own, answers unchanged.
	f0, p0 := count("fetch"), count("push")
	rr, mr, rf, wr = storeChecks(ctxT(t), p, url, Route{})
	check("no memo", rr, mr, rf, wr)
	if f, pu := count("fetch")-f0, count("push")-p0; f != 3 || pu != 2 {
		t.Errorf("without a memo: %d fetch, %d push; want 3, 2", f, pu)
	}
	assertTmpClean(t, s.tmp)
}

// A shared failure is shared too (one connection, every check gets the
// class), and an empty remote is ok + empty for read, marker and root files.
func TestRemoteMemoFailureAndEmpty(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	s := newSandbox(t)
	root := t.TempDir()
	empty := filepath.Join(root, "empty.git")
	bareRepo(t, empty)
	count := countingGit(t, s, realGit)
	p := s.prober(Options{Limiter: NewLimiter(8)})

	ctx := WithRemoteMemo(ctxT(t))
	rr, mr, rf, _ := storeChecks(ctx, p, fileURL(empty), Route{})
	if rr.Class != ClassOK || !rr.Empty || len(rr.Heads) != 0 {
		t.Errorf("read empty: %+v", rr)
	}
	if mr.Class != ClassOK || mr.Present || !mr.EmptyRepo {
		t.Errorf("marker empty: %+v", mr)
	}
	if rf.Class != ClassOK || len(rf.Found) != 0 || !rf.EmptyRepo {
		t.Errorf("root files empty: %+v", rf)
	}

	before := count("fetch")
	missing := fileURL(filepath.Join(root, "nothere.git"))
	rr, mr, rf, _ = storeChecks(ctx, p, missing, Route{})
	if rr.Class != ClassNoAccess || mr.Class != ClassNoAccess || rf.Class != ClassNoAccess {
		t.Errorf("missing repo: read %+v, marker %+v, root %+v", rr, mr, rf)
	}
	if got := count("fetch") - before; got != 1 {
		t.Errorf("missing repo fetched %d times, want 1", got)
	}
	assertTmpClean(t, s.tmp)
}

// A check that waits for another's fetch gives up when its own context
// ends (a timeout outcome), and never takes a probe slot while it waits.
func TestRemoteMemoWaiterTimesOut(t *testing.T) {
	m := map[snapKey]*flight[remoteSnapshot]{}
	var mu sync.Mutex
	key := snapKey{url: "file:///x.git"}
	release := make(chan struct{})
	started := make(chan struct{})
	go share(context.Background(), &mu, m, key, "leader", func() remoteSnapshot {
		close(started)
		<-release
		return remoteSnapshot{Outcome: Outcome{Class: ClassOK}}
	})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, ok := share(ctx, &mu, m, key, "waiter", func() remoteSnapshot {
		t.Error("the waiter ran its own compute")
		return remoteSnapshot{}
	}); ok {
		t.Error("the waiter got an answer after its context ended")
	}
	close(release)
	// After the leader is done, a new caller gets its answer at once.
	if v, shared, ok := share(context.Background(), &mu, m, key, "late", func() remoteSnapshot {
		t.Error("the late caller ran its own compute")
		return remoteSnapshot{}
	}); !ok || !shared || v.Class != ClassOK {
		t.Errorf("late caller: %+v shared=%v ok=%v", v, shared, ok)
	}
}
