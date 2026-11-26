package git

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// bloblessRemote is a store remote with history: one row written in three
// versions by a full clone, the remote reached by a file:// url (a plain
// path takes git's local-clone shortcut, which ignores a filter). filter
// says whether the remote serves partial clones.
type bloblessRemote struct {
	bare   string // the bare repository's path
	url    string // file:// url of it
	writer *Backend
	id     ids.AnyID
	g      *gen
	last   hlc.HLC
}

func newBloblessRemote(t *testing.T, filter bool) *bloblessRemote {
	t.Helper()
	r := &bloblessRemote{bare: newRemote(t, true), g: newGen(t), id: newID(t)}
	if filter {
		gitT(t, r.bare, "config", "uploadpack.allowFilter", "true")
		gitT(t, r.bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	}
	r.url = "file://" + filepath.ToSlash(r.bare)
	r.writer = clone(t, r.url)
	for _, body := range []string{"widget v1", "widget v2", "widget v3"} {
		r.put(t, body)
	}
	return r
}

// put writes one more version of the row and pushes it.
func (r *bloblessRemote) put(t *testing.T, body string) {
	t.Helper()
	row := r.g.row(r.id, body, r.last)
	write(t, r.writer, store.Change{Row: row, Expect: r.last})
	r.last = row.HLC
	syncOK(t, r.writer)
}

// missingIn counts the objects reachable from the clone's refs that the
// clone does not hold, without fetching any.
func missingIn(t *testing.T, dir string) int {
	t.Helper()
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runner{bin: bin}.run(context.Background(), call{dir: dir, args: []string{"rev-list", "--objects", "--missing=print", "--all"}, env: []string{noLazyFetchEnv}})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for l := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(l, "?") {
			n++
		}
	}
	return n
}

// openBlobless opens a new clone of url with store.clone_filter blob:none.
func openBlobless(t *testing.T, url string) *Backend {
	t.Helper()
	return clone(t, url, WithCloneFilter(CloneFilterBlobless))
}

// fetchTracer records the git fetch calls lore itself runs.
type fetchTracer struct {
	mu    sync.Mutex
	calls [][]string
}

func (f *fetchTracer) ctx() context.Context {
	return gitref.WithTracer(context.Background(), func(c gitref.TraceCall) {
		if gitenv.Subcommand(c.Args) == "fetch" {
			f.mu.Lock()
			f.calls = append(f.calls, c.Args)
			f.mu.Unlock()
		}
	})
}

// TestBloblessCloneHoldsNoOldBlobs: with store.clone_filter blob:none a
// new store clone gets the promisor config `git clone --filter` writes and
// holds every commit and tree but only the blobs it checked out; history a
// later sync brings comes with every blob (--no-filter). Break-verified:
// without withNoFilter in runOnce the sync left the new versions' blobs
// missing (store growth SG-3b, devco:D-1001).
func TestBloblessCloneHoldsNoOldBlobs(t *testing.T) {
	r := newBloblessRemote(t, true)
	b := openBlobless(t, r.url)
	if got := gitT(t, b.work, "config", "--get", "remote.origin.promisor"); got != "true" {
		t.Fatalf("remote.origin.promisor = %q; want true", got)
	}
	if got := gitT(t, b.work, "config", "--get", "remote.origin.partialclonefilter"); got != CloneFilterBlobless {
		t.Fatalf("remote.origin.partialclonefilter = %q; want blob:none", got)
	}
	if !IsPartialClone(b.work) {
		t.Fatal("IsPartialClone = false for a blobless clone")
	}
	before := missingIn(t, b.work)
	if before < 2 {
		t.Fatalf("a new blobless clone lacks %d objects; want the row's two old versions missing", before)
	}
	r.put(t, "widget v4")
	r.put(t, "widget v5")
	syncOK(t, b)
	if after := missingIn(t, b.work); after != before {
		t.Fatalf("after a sync of two new versions the clone lacks %d objects, was %d: the new history came without its blobs (want --no-filter)", after, before)
	}
}

// TestBloblessReadFetchesMissingBlobsInOneRequest: the first read of a
// blobless clone (the roster replay reads every row version) fetches the
// versions it lacks in ONE fetch, never one lazy fetch per blob, and reads
// the same row a full clone reads. Break-verified: the old readBlobs ran
// no fetch of its own (git fetched each blob lazily inside cat-file).
func TestBloblessReadFetchesMissingBlobsInOneRequest(t *testing.T) {
	r := newBloblessRemote(t, true)
	b := openBlobless(t, r.url)
	if missingIn(t, b.work) == 0 {
		t.Fatal("the blobless clone lacks nothing; the test needs old versions missing")
	}
	var tr fetchTracer
	got, err := b.Get(tr.ctx(), memories, r.id)
	if err != nil {
		t.Fatal(err)
	}
	if want := get(t, r.writer, r.id); !slices.Equal(got.Doc, want.Doc) {
		t.Fatalf("the blobless clone read %q; the full clone reads %q", got.Doc, want.Doc)
	}
	var batched int
	for _, c := range tr.calls {
		if slices.Contains(c, "--stdin") {
			batched++
		}
	}
	if batched != 1 {
		t.Fatalf("the first read ran %d batched fetch(es) of missing versions (calls %v); want exactly 1", batched, tr.calls)
	}
	if n := missingIn(t, b.work); n != 0 {
		t.Fatalf("after the replay read every version the clone still lacks %d objects", n)
	}
}

// TestBloblessCloneServerWithoutFilter: a remote that does not serve
// partial clones sends every object; the clone drops the promisor config
// and is a full clone, so nothing later takes it for blobless.
// Break-verified: without noteFilterHonoured the promisor config stayed.
func TestBloblessCloneServerWithoutFilter(t *testing.T) {
	r := newBloblessRemote(t, false)
	b := openBlobless(t, r.url)
	if IsPartialClone(b.work) {
		t.Fatal("a clone of a remote without filter support kept the promisor config")
	}
	if n := missingIn(t, b.work); n != 0 {
		t.Fatalf("the full clone lacks %d objects", n)
	}
}

// TestFullCloneStaysFull: without the setting a new clone is a full clone
// and its fetches carry no filter option at all.
func TestFullCloneStaysFull(t *testing.T) {
	r := newBloblessRemote(t, true)
	b := clone(t, r.url)
	if IsPartialClone(b.work) || missingIn(t, b.work) != 0 {
		t.Fatal("a clone without store.clone_filter is not a full clone")
	}
	var tr fetchTracer
	if _, err := b.Sync(tr.ctx()); err != nil {
		t.Fatal(err)
	}
	for _, c := range tr.calls {
		for _, a := range c {
			if strings.Contains(a, "filter") {
				t.Fatalf("a full clone's fetch carries %q: %v", a, c)
			}
		}
	}
}

// TestPrefetchFetchesEveryOldBlob: lore store prefetch fills a blobless
// clone in one request, after which no history read needs the network;
// offline it refuses with E_OFFLINE.
func TestPrefetchFetchesEveryOldBlob(t *testing.T) {
	r := newBloblessRemote(t, true)
	b := openBlobless(t, r.url)
	t.Run("offline", func(t *testing.T) {
		t.Setenv(gitenv.EnvOffline, gitenv.On)
		if _, err := b.Prefetch(context.Background()); !errs.Has(err, errs.Offline) {
			t.Fatalf("Prefetch offline = %v; want E_OFFLINE", err)
		}
	})
	missing := missingIn(t, b.work)
	res, err := b.Prefetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Partial || res.Missing != missing || res.Fetched != missing || missingIn(t, b.work) != 0 {
		t.Fatalf("Prefetch = %+v with %d missing before, %d after; want every one fetched", res, missing, missingIn(t, b.work))
	}
	full := clone(t, r.url)
	if res, err := full.Prefetch(context.Background()); err != nil || res.Partial {
		t.Fatalf("Prefetch of a full clone = %+v, %v; want nothing to do", res, err)
	}
}

// TestRepointOriginBackfillsBloblessClone: store set-url points a clone at
// another repository and its next sync pushes the whole history there;
// a blobless clone first fetches every old version from the old remote
// (the only place it lives), and offline nothing changes. Break-verified:
// without the backfill the clone still lacked the old versions after the
// repoint, and its promisor remote was the new, empty repository.
func TestRepointOriginBackfillsBloblessClone(t *testing.T) {
	r := newBloblessRemote(t, true)
	b := openBlobless(t, r.url)
	moved := filepath.Join(t.TempDir(), "moved.git")
	gitT(t, filepath.Dir(moved), "init", "--quiet", "--bare", "--initial-branch="+testBranch, moved)
	t.Run("offline", func(t *testing.T) {
		t.Setenv(gitenv.EnvOffline, gitenv.On)
		if err := RepointOrigin(context.Background(), b.work, moved); !errs.Has(err, errs.Offline) {
			t.Fatalf("RepointOrigin offline = %v; want E_OFFLINE", err)
		}
		if got := gitT(t, b.work, "config", "--get", "remote.origin.url"); got != r.url {
			t.Fatalf("origin after a refused repoint = %q; want it unchanged %q", got, r.url)
		}
	})
	if err := RepointOrigin(context.Background(), b.work, moved); err != nil {
		t.Fatal(err)
	}
	if n := missingIn(t, b.work); n != 0 {
		t.Fatalf("after the repoint the clone lacks %d old versions that only the old remote has", n)
	}
}

// TestLocalOnlyCommits counts what a reclone would lose: a commit not
// pushed yet, none once it is.
func TestLocalOnlyCommits(t *testing.T) {
	r := newBloblessRemote(t, true)
	ctx := context.Background()
	if n, err := r.writer.LocalOnlyCommits(ctx); err != nil || n != 0 {
		t.Fatalf("LocalOnlyCommits of a pushed clone = %d, %v; want 0", n, err)
	}
	row := r.g.row(r.id, "widget local", r.last)
	write(t, r.writer, store.Change{Row: row, Expect: r.last})
	if n, err := r.writer.LocalOnlyCommits(ctx); err != nil || n != 1 {
		t.Fatalf("LocalOnlyCommits with one unpushed write = %d, %v; want 1", n, err)
	}
}

// TestWithNoFilter: --no-filter goes right after the fetch subcommand,
// past -c pairs, and never where a filter or an object list was chosen.
func TestWithNoFilter(t *testing.T) {
	for _, c := range []struct{ in, want []string }{
		{[]string{"fetch", "--quiet", "origin"}, []string{"fetch", "--no-filter", "--quiet", "origin"}},
		{[]string{"-c", "remote.origin.fetch=x", "fetch", "origin"}, []string{"-c", "remote.origin.fetch=x", "fetch", "--no-filter", "origin"}},
		{[]string{"fetch", "--filter=blob:none", "origin"}, []string{"fetch", "--filter=blob:none", "origin"}},
		{[]string{"fetch", "--stdin", "origin"}, []string{"fetch", "--stdin", "origin"}},
		{[]string{"fetch", "--no-filter", "origin"}, []string{"fetch", "--no-filter", "origin"}},
		{[]string{"push", "origin"}, []string{"push", "origin"}},
	} {
		if got := withNoFilter(c.in); !slices.Equal(got, c.want) {
			t.Errorf("withNoFilter(%v) = %v; want %v", c.in, got, c.want)
		}
	}
}
