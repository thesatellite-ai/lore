package gitref

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// countingGit counts the invocations that reach the real git, by their
// first argument (the subcommand), so a test can tell a cache hit from a run.
type countingGit struct {
	inner Git
	mu    sync.Mutex
	runs  map[string]int
}

func newCountingGit() *countingGit { return &countingGit{inner: Exec{}, runs: map[string]int{}} }

func (g *countingGit) Run(ctx context.Context, inv Invocation) ([]byte, error) {
	g.mu.Lock()
	if len(inv.Args) > 0 {
		g.runs[inv.Args[0]]++
	}
	g.mu.Unlock()
	return g.inner.Run(ctx, inv)
}

func (g *countingGit) count(sub string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.runs[sub]
}

// cachedRepo opens a fresh normal repository through a ReadCache over a
// countingGit.
func cachedRepo(t *testing.T) (*Repo, *ReadCache, *countingGit) {
	t.Helper()
	plain := newRepo(t, kindNormal)
	counter := newCountingGit()
	cache := NewReadCache(counter)
	r, err := Open(context.Background(), plain.Dir(), cache)
	if err != nil {
		t.Fatal(err)
	}
	return r, cache, counter
}

// TestReadCacheAnswersRepeatedReadsOnce: the same read asked again runs git
// once (the per-row storm a lore operation makes), for a found ref, a
// missing ref (a non-zero exit is an answer too), a tree read and a
// symbolic-ref read.
func TestReadCacheAnswersRepeatedReadsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _, counter := cachedRepo(t)
	const asks = 5
	for range asks {
		if _, ok, err := r.ResolveRef(ctx, "refs/heads/main"); err != nil || !ok {
			t.Fatalf("ResolveRef main = %v %v", ok, err)
		}
		if _, ok, err := r.ResolveRef(ctx, "refs/lore/live"); err != nil || ok {
			t.Fatalf("ResolveRef missing = %v %v", ok, err)
		}
		if _, ok, err := r.ResolveCommit(ctx, "refs/lore/nothing"); err != nil || ok {
			t.Fatalf("ResolveCommit missing = %v %v", ok, err)
		}
		if b, ok, err := r.ReadFile(ctx, "HEAD", "seed.txt"); err != nil || !ok || string(b) != "seed\n" {
			t.Fatalf("ReadFile = %q %v %v", b, ok, err)
		}
		if out, err := r.Run(ctx, Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}}); err != nil || strings.TrimSpace(string(out)) != "main" {
			t.Fatalf("symbolic-ref = %q %v", out, err)
		}
	}
	// two names checked, two refs listed, two rev-parses per ReadFile
	// round plus the missing commit (once each), one ls-tree, one cat-file
	want := map[string]int{"check-ref-format": 2, "for-each-ref": 2, "ls-tree": 1, "cat-file": 1, "symbolic-ref": 1}
	for sub, n := range want {
		if got := counter.count(sub); got != n {
			t.Errorf("%s ran %d times for %d asks, want %d", sub, got, asks, n)
		}
	}
	// Open's rev-parse, the missing commit and ReadFile's HEAD^{commit}
	if got := counter.count("rev-parse"); got != 3 {
		t.Errorf("rev-parse ran %d times, want 3", got)
	}
}

// TestReadCacheWriteDropsReads: a write through the cache (WriteRef moves a
// ref) makes every later read see it: the ref's new tip, the new tree, and
// a ref that did not exist before. check-ref-format answers are kept.
func TestReadCacheWriteDropsReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _, counter := cachedRepo(t)
	const ref = "refs/lore/live"
	if _, ok, err := r.ResolveRef(ctx, ref); err != nil || ok {
		t.Fatalf("before: %v %v", ok, err)
	}
	if _, ok, err := r.ReadFile(ctx, ref, "a.txt"); err == nil || ok {
		t.Fatalf("before: the ref has no tree yet, got %v %v", ok, err)
	}
	for i, body := range []string{"one\n", "two\n"} {
		res, err := r.WriteRef(ctx, WriteRequest{Ref: ref, Changes: Changes{Put: map[string][]byte{"a.txt": []byte(body)}}, Message: "w", Signature: testSig})
		if err != nil || !res.Changed {
			t.Fatalf("write %d: %+v %v", i, res, err)
		}
		oid, ok, err := r.ResolveRef(ctx, ref)
		if err != nil || !ok || oid != res.New {
			t.Fatalf("after write %d: ResolveRef = %s %v %v, want %s", i, oid, ok, err, res.New)
		}
		b, ok, err := r.ReadFile(ctx, ref, "a.txt")
		if err != nil || !ok || string(b) != body {
			t.Fatalf("after write %d: ReadFile = %q %v %v, want %q", i, b, ok, err, body)
		}
	}
	if got := counter.count("check-ref-format"); got != 1 {
		t.Errorf("check-ref-format ran %d times, want 1 (its answer never goes stale)", got)
	}
}

// TestReadCacheInvalidate: a write made outside the cache (another
// process) is not seen until Invalidate, and is seen right after it.
func TestReadCacheInvalidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, cache, _ := cachedRepo(t)
	before, _, err := r.ResolveRef(ctx, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	git(t, r.Dir(), "commit", "-q", "--allow-empty", "-m", "elsewhere")
	after := git(t, r.Dir(), "rev-parse", "refs/heads/main")
	if got, _, _ := r.ResolveRef(ctx, "refs/heads/main"); got != before {
		t.Fatalf("without Invalidate the answer is the cached one: got %s want %s", got, before)
	}
	cache.Invalidate()
	if got, _, _ := r.ResolveRef(ctx, "refs/heads/main"); got != after {
		t.Fatalf("after Invalidate: got %s want %s", got, after)
	}
}

// TestReadCacheAnswersAreCopies: a caller that changes the bytes it got
// back cannot change what the next caller gets.
func TestReadCacheAnswersAreCopies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _, _ := cachedRepo(t)
	args := Invocation{Args: []string{"rev-parse", "HEAD"}}
	first, err := r.Run(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	want := string(first)
	for i := range first {
		first[i] = 'x'
	}
	second, err := r.Run(ctx, args)
	if err != nil || string(second) != want {
		t.Fatalf("second answer = %q %v, want %q", second, err, want)
	}
}

// TestReadCacheKeepsNoCancelledAnswer: a run whose context was cancelled
// is passed through and not remembered.
func TestReadCacheKeepsNoCancelledAnswer(t *testing.T) {
	t.Parallel()
	r, _, counter := cachedRepo(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	inv := Invocation{Args: []string{"rev-parse", "HEAD"}}
	if _, err := r.Run(cancelled, inv); err == nil {
		t.Fatal("a cancelled run must fail")
	}
	before := counter.count("rev-parse")
	if _, err := r.Run(context.Background(), inv); err != nil {
		t.Fatalf("the next run must run git and succeed: %v", err)
	}
	if counter.count("rev-parse") != before+1 {
		t.Fatal("the failed cancelled run was remembered")
	}
}

// gateGit holds the answer of the first for-each-ref until release is
// closed: git has already answered (the old state), and a test can run a
// write before that answer reaches the cache.
type gateGit struct {
	inner   Git
	entered chan struct{}
	release chan struct{}
	first   atomic.Bool
}

func (g *gateGit) Run(ctx context.Context, inv Invocation) ([]byte, error) {
	out, err := g.inner.Run(ctx, inv)
	// Only the first for-each-ref waits; the write's own reads pass.
	if len(inv.Args) > 0 && inv.Args[0] == "for-each-ref" && g.first.CompareAndSwap(false, true) {
		close(g.entered)
		<-g.release
	}
	return out, err
}

// TestReadCacheOverlappingReadIsNotKept: a read that started before a write
// and finished after it may carry the old answer, so it is returned but
// never stored; the next read runs git and sees the write.
func TestReadCacheOverlappingReadIsNotKept(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	plain := newRepo(t, kindNormal)
	gate := &gateGit{inner: Exec{}, entered: make(chan struct{}), release: make(chan struct{})}
	r, err := Open(ctx, plain.Dir(), NewReadCache(gate))
	if err != nil {
		t.Fatal(err)
	}
	const ref = "refs/lore/live"
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = r.ResolveRef(ctx, ref) // answered before the write, returned after it
	}()
	<-gate.entered
	res, err := r.WriteRef(ctx, WriteRequest{Ref: ref, Changes: Changes{Put: map[string][]byte{"a.txt": []byte("a")}}, Message: "w", Signature: testSig})
	if err != nil {
		t.Fatal(err)
	}
	close(gate.release)
	<-done
	oid, ok, err := r.ResolveRef(ctx, ref)
	if err != nil || !ok || oid != res.New {
		t.Fatalf("after the overlapping read: %s %v %v, want %s", oid, ok, err, res.New)
	}
}

// TestReadCacheConcurrentUse: readers and writers on one cache at once
// (run with -race); every reader that starts after a write returned sees
// at least that write.
func TestReadCacheConcurrentUse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _, _ := cachedRepo(t)
	const ref, writes, readers = "refs/lore/live", 6, 4
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, _, err := r.ResolveRef(ctx, ref); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := range writes {
		res, err := r.WriteRef(ctx, WriteRequest{Ref: ref, Changes: Changes{Put: map[string][]byte{"a.txt": []byte{byte('a' + i)}}}, Message: "w", Signature: testSig})
		if err != nil {
			t.Fatal(err)
		}
		if oid, _, err := r.ResolveRef(ctx, ref); err != nil || oid != res.New {
			t.Fatalf("write %d: read back %s %v, want %s", i, oid, err, res.New)
		}
	}
	close(stop)
	wg.Wait()
}

// TestReadCacheClassify pins which invocations are reads: anything not
// recognised is a write, so it can only make the cache drop answers.
func TestReadCacheClassify(t *testing.T) {
	t.Parallel()
	cases := []struct {
		inv  Invocation
		want readClass
	}{
		{Invocation{Args: []string{"check-ref-format", "refs/lore/live"}}, classPure},
		{Invocation{Args: []string{"for-each-ref", "--", "refs/lore/live"}}, classRead},
		{Invocation{Args: []string{"ls-tree", "-r", "HEAD"}}, classRead},
		{Invocation{Args: []string{"rev-parse", "--verify", "HEAD"}}, classRead},
		{Invocation{Args: []string{"cat-file", "--batch"}, Stdin: []byte("x\n")}, classRead},
		{Invocation{Args: []string{"merge-base", "a", "b"}}, classRead},
		{Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}}, classRead},
		{Invocation{Args: []string{"symbolic-ref", "HEAD", "refs/heads/x"}}, classWrite},
		{Invocation{Args: []string{"symbolic-ref", "-d", "HEAD"}}, classWrite},
		{Invocation{Args: []string{"symbolic-ref", "-m", "why", "HEAD", "refs/heads/x"}}, classWrite},
		{Invocation{Args: []string{"update-ref", "refs/lore/live", "x"}}, classWrite},
		{Invocation{Args: []string{"config", "--get", "user.email"}}, classWrite},
		{Invocation{Args: []string{"fetch", "origin"}}, classWrite},
		{Invocation{Args: []string{"-C", "/elsewhere", "rev-parse", "HEAD"}}, classWrite},
		{Invocation{Args: []string{"rev-parse", "HEAD"}, Env: []string{"GIT_INDEX_FILE=x"}}, classWrite},
		{Invocation{}, classWrite},
	}
	for _, c := range cases {
		if got := classify(c.inv); got != c.want {
			t.Errorf("classify(%q env %q) = %d, want %d", c.inv.Args, c.inv.Env, got, c.want)
		}
	}
}
