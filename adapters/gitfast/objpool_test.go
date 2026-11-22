package gitfast

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestObjectStoresKeptAcrossSessions (devco:D-838): in a process that
// shares object stores (the read server), a second Session reads objects
// from the store the first one opened, with its decoded objects, and
// Session.Close leaves it open; a repack (the old pack gone) or a stop
// gives a fresh store. Answers match git throughout. Not parallel: the
// pool is process state.
func TestObjectStoresKeptAcrossSessions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, _, _ := fixture(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	blob := gitT(t, work, "rev-parse", "HEAD~3:org/tasks/t1.json") // packed, a delta
	args := []string{"cat-file", "blob", blob}
	want, _ := realGit(t, work, args, nil)
	storeOf := func(s *Session) *objectStore {
		t.Helper()
		r := s.repoFor(work)
		if r == nil {
			t.Fatal("no repository read")
		}
		objs, err := r.objects()
		if err != nil {
			t.Fatal(err)
		}
		return objs
	}
	answer := func(s *Session, what string) {
		t.Helper()
		rep, ok := s.Answer(ctx, work, args, nil, nil)
		if !ok || rep.Code != 0 || !bytes.Equal(rep.Out, want) {
			t.Fatalf("%s: answered %v (exit %d) %q, want git's %q", what, ok, rep.Code, rep.Out, want)
		}
	}

	stop := ShareObjectStores()
	s1 := New()
	answer(s1, "first Session")
	first := storeOf(s1)
	s1.Close()
	s2 := New()
	if storeOf(s2) != first {
		t.Fatal("a second Session opened the object folder again instead of the kept store")
	}
	first.mu.Lock()
	_, decoded := first.cache[blob]
	first.mu.Unlock()
	if !decoded {
		t.Fatal("the kept store lost the object the first Session decoded")
	}
	answer(s2, "second Session (after the first closed)")
	s2.Close()

	gitT(t, work, "repack", "-a", "-d", "-q", "-f") // the kept store's pack is gone
	s3 := New()
	if storeOf(s3) == first {
		t.Fatal("the kept store was reused after its pack was deleted by a repack")
	}
	answer(s3, "after a repack")
	s3.Close()
	stop()

	s4, s5 := New(), New()
	defer s4.Close()
	defer s5.Close()
	if storeOf(s4) == storeOf(s5) {
		t.Fatal("without sharing, two Sessions share an object store")
	}
	answer(s5, "without sharing")
}

// TestKeptObjectStoreBoundedByBytes (devco:D-845): a kept store counted
// its decoded objects (200k), not their bytes, so one store could hold
// hundreds of MB in the read server. It counts the bytes it decodes and
// is dropped (a fresh one opened) once they pass objPoolMaxBytes. Not
// parallel: the pool is process state.
func TestKeptObjectStoreBoundedByBytes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, _, _ := fixture(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	blob := gitT(t, work, "rev-parse", "HEAD~3:org/tasks/t1.json")
	stop := ShareObjectStores()
	defer stop()
	storeOf := func() *objectStore {
		t.Helper()
		s := New()
		defer s.Close()
		r := s.repoFor(work)
		if r == nil {
			t.Fatal("no repository read")
		}
		objs, err := r.objects()
		if err != nil {
			t.Fatal(err)
		}
		return objs
	}
	kept := storeOf()
	o, err := kept.read(blob)
	if err != nil {
		t.Fatal(err)
	}
	kept.mu.Lock()
	counted := kept.bytes
	kept.mu.Unlock()
	if counted < len(o.data) || len(o.data) == 0 {
		t.Fatalf("the kept store counts %d bytes after decoding a %d-byte object", counted, len(o.data))
	}
	kept.mu.Lock()
	kept.bytes = objPoolMaxBytes // at the bound: still kept
	kept.mu.Unlock()
	if storeOf() != kept {
		t.Fatal("a store at its byte bound was dropped")
	}
	kept.mu.Lock()
	kept.bytes = objPoolMaxBytes + 1 // one more decoded byte
	kept.mu.Unlock()
	if fresh := storeOf(); fresh == kept {
		t.Fatalf("a store holding %d decoded bytes (bound %d) was kept", kept.bytes, objPoolMaxBytes)
	}
}

// TestKeptObjectStoreDropsReplacedTrees (devco:D-866): every write to a
// big folder makes a new version of its table tree, and the kept store of
// the read server parsed and kept each version for good (tree index, raw
// tree, commit), so its heap grew ~1.3 MB per write to a 5,000-row folder
// until the server exited on its live-heap limit. Now many write-then-read
// rounds keep the store's trees, its counted bytes and the live heap
// within a fixed margin: only the versions recent calls read stay. Not
// parallel: the pool is process state (and the heap is measured).
func TestKeptObjectStoreDropsReplacedTrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	work := filepath.Join(t.TempDir(), "work")
	gitT(t, filepath.Dir(work), "init", "-q", "-b", "main", work)
	rows := filepath.Join(work, "feeds", "memories")
	if err := os.MkdirAll(rows, 0o755); err != nil {
		t.Fatal(err)
	}
	const n = 3000
	for i := range n {
		if err := os.WriteFile(filepath.Join(rows, fmt.Sprintf("mem_%020x.json", i)), []byte(fmt.Sprintf(`{"body":"event %d"}`, i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-q", "-m", "rows")

	stop := ShareObjectStores()
	defer stop()
	ctx := context.Background()
	var kept *objectStore
	// round writes one row (a new commit and a new feeds/memories tree)
	// and reads it back through a new Session, as one served call does.
	round := func(i int) {
		t.Helper()
		name := fmt.Sprintf("feeds/memories/mem_new_%04d.json", i)
		body := fmt.Sprintf(`{"body":"write %d"}`, i)
		if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		gitT(t, work, "add", "-A")
		gitT(t, work, "commit", "-q", "-m", "write")
		s := New()
		defer s.Close()
		rep, ok := s.Answer(ctx, work, []string{"cat-file", "--batch"}, nil, []byte("HEAD:"+name+"\n"))
		if !ok || rep.Code != 0 || !bytes.HasSuffix(bytes.TrimSpace(rep.Out), []byte(body)) {
			t.Fatalf("round %d: answered %v (exit %d) %q, want the row just written", i, ok, rep.Code, rep.Out)
		}
		objs, err := s.repoFor(work).objects()
		if err != nil {
			t.Fatal(err)
		}
		if kept == nil {
			kept = objs
		} else if objs != kept {
			t.Fatalf("round %d: the kept store was dropped (bytes bound?), so this test measures nothing", i)
		}
	}
	live := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	stored := func() (trees, others, counted int) {
		kept.mu.Lock()
		defer kept.mu.Unlock()
		for _, c := range kept.cache {
			if c.o.typ != typeBlob {
				others++
			}
		}
		return len(kept.trees), others, kept.bytes
	}

	const warm, rounds = keepUses + 2, 40
	for i := range warm {
		round(i)
	}
	_, _, bytes0 := stored()
	heap0 := live()
	for i := warm; i < rounds; i++ {
		round(i)
	}
	trees, others, bytes1 := stored()
	heap1 := live()
	// three trees (root, feeds, feeds/memories) and a commit per version;
	// the versions of the last keepUses+1 uses at most
	if maxTrees := 3 * (keepUses + 1); trees > maxTrees {
		t.Errorf("%d tree indexes kept after %d write+read rounds, want at most %d (the versions recent calls read)", trees, rounds, maxTrees)
	}
	if maxOthers := 4 * (keepUses + 1); others > maxOthers {
		t.Errorf("%d trees and commits kept after %d write+read rounds, want at most %d", others, rounds, maxOthers)
	}
	// the new row blobs are all that may add up (a few dozen bytes each)
	if grew := bytes1 - bytes0; grew > 64<<10 {
		t.Errorf("the kept store's counted bytes grew %d over %d rounds (from %d to %d), want flat", grew, rounds-warm, bytes0, bytes1)
	}
	if heap1 > heap0 && heap1-heap0 > 4<<20 {
		t.Errorf("live heap grew %.1f MB over %d write+read rounds of a %d-row folder, want flat", float64(heap1-heap0)/(1<<20), rounds-warm, n)
	}
}

// TestKeptBytesSumsKeptStores (devco:D-887): the read server sizes its
// memory limits to what it keeps, so KeptBytes is the bytes the shared
// object stores hold, and 0 once sharing stops. Not parallel: the pool is
// process state.
func TestKeptBytesSumsKeptStores(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, _, _ := fixture(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	blob := gitT(t, work, "rev-parse", "HEAD~3:org/tasks/t1.json")
	stop := ShareObjectStores()
	if got := KeptBytes(); got != 0 {
		t.Fatalf("KeptBytes = %d before any read, want 0", got)
	}
	s := New()
	if rep, ok := s.Answer(ctx, work, []string{"cat-file", "blob", blob}, nil, nil); !ok || rep.Code != 0 {
		t.Fatalf("cat-file answered %v (exit %d)", ok, rep.Code)
	}
	objs := s.repoFor(work)
	if objs == nil {
		t.Fatal("no repository read")
	}
	store, err := objs.objects()
	if err != nil {
		t.Fatal(err)
	}
	s.Close() // the kept store stays open
	store.mu.Lock()
	want := int64(store.bytes)
	store.mu.Unlock()
	if got := KeptBytes(); want == 0 || got != want {
		t.Fatalf("KeptBytes = %d, want the kept store's %d bytes (non-zero)", got, want)
	}
	stop()
	if got := KeptBytes(); got != 0 {
		t.Fatalf("KeptBytes = %d after sharing stopped, want 0", got)
	}
}
