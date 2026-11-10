package gitref

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// recordingObserver remembers the subcommands it was told about, in order.
type recordingObserver struct {
	mu   sync.Mutex
	subs []string
}

func (o *recordingObserver) observe(inv Invocation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.subs = append(o.subs, strings.Join(inv.Args, " "))
}

func (o *recordingObserver) seen() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.subs...)
}

// TestExecTellsTheObserverAboutWrites: Exec under a context with a
// WriteObserver calls it before and after every command ReadCache would not
// keep (a ref update, a global option, extra Env), and never for a read it
// keeps, so the observer's owner drops its cache exactly when the cache
// itself would have.
func TestExecTellsTheObserverAboutWrites(t *testing.T) {
	t.Parallel()
	plain := newRepo(t, kindNormal)
	obs := &recordingObserver{}
	ctx := WithWriteObserver(context.Background(), obs.observe)
	head, ok, err := plain.ResolveRef(ctx, "refs/heads/main")
	if err != nil || !ok {
		t.Fatalf("ResolveRef: %v %v", ok, err)
	}
	if got := obs.seen(); len(got) != 0 {
		t.Fatalf("a read must not reach the observer: %q", got)
	}
	cases := []struct {
		name string
		inv  Invocation
	}{
		{"update-ref", Invocation{Dir: plain.Dir(), Args: []string{"update-ref", "refs/heads/side", head}}},
		{"global option", Invocation{Dir: plain.Dir(), Args: []string{"-c", "core.quotePath=false", "rev-parse", "HEAD"}}},
		{"env", Invocation{Dir: plain.Dir(), Args: []string{"rev-parse", "HEAD"}, Env: []string{"GIT_AUTHOR_NAME=x"}}},
		{"check-ref-format is pure", Invocation{Args: []string{"check-ref-format", "refs/heads/ok"}}},
	}
	for _, c := range cases {
		before := len(obs.seen())
		if _, err := (Exec{}).Run(ctx, c.inv); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := obs.seen()[before:]
		want := 2
		if c.name == "check-ref-format is pure" {
			want = 0
		}
		if len(got) != want {
			t.Fatalf("%s: observer calls %q, want %d", c.name, got, want)
		}
	}
	if _, err := (Exec{}).Run(context.Background(), Invocation{Dir: plain.Dir(), Args: []string{"update-ref", "refs/heads/other", head}}); err != nil {
		t.Fatal(err)
	}
	if got := len(obs.seen()); got != 2*3 {
		t.Fatalf("a context without the observer must not reach it: %d calls", got)
	}
}

// TestReadCacheOwnerSeesWritesMadeBeside: the case WriteObserver exists
// for. A write through a Repo opened without the cache (the signals push of
// a claim, reposync's promote) under a context whose observer invalidates
// the cache is seen by the next cached read; without that observer the
// cached answer from before the write would be served.
func TestReadCacheOwnerSeesWritesMadeBeside(t *testing.T) {
	t.Parallel()
	r, cache, _ := cachedRepo(t)
	ctx := WithWriteObserver(context.Background(), func(Invocation) { cache.Invalidate() })
	before, ok, err := r.ResolveRef(ctx, "refs/heads/main")
	if err != nil || !ok {
		t.Fatalf("ResolveRef: %v %v", ok, err)
	}
	beside, err := Open(ctx, r.Dir(), nil) // plain Exec, not the cache
	if err != nil {
		t.Fatal(err)
	}
	if _, err := beside.Run(ctx, Invocation{Args: []string{"commit", "-q", "--allow-empty", "-m", "beside"},
		Env: []string{"GIT_AUTHOR_NAME=b", "GIT_AUTHOR_EMAIL=b@example.com", "GIT_COMMITTER_NAME=b", "GIT_COMMITTER_EMAIL=b@example.com"}}); err != nil {
		t.Fatalf("commit beside the cache: %v", err)
	}
	after, ok, err := r.ResolveRef(ctx, "refs/heads/main")
	if err != nil || !ok || after == before {
		t.Fatalf("the cached read must see the write made beside it: got %s (before %s) %v %v", after, before, ok, err)
	}
}
