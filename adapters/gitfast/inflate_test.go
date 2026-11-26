package gitfast

import (
	"bytes"
	"compress/zlib"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestInflateReusesReaders (bug hunt H-277, devco:D-1015): reading many
// packed objects reuses zlib readers instead of making one per object.
// A new reader allocates its 32 KB window and an input buffer: lore
// doctor on a 100k-commit store allocated 6.4 GB that way in a 12 s run,
// and the heap between collections, so the process's memory, grew to
// several times what it kept. Every object still inflates to its bytes.
// Not parallel: it measures the process's allocations.
func TestInflateReusesReaders(t *testing.T) {
	const reads = 200
	var objs [][]byte
	var pack bytes.Buffer
	var offs []int64
	for i := range 4 {
		obj := []byte(strings.Repeat("a row document, read again and again ", 8+i))
		offs = append(offs, int64(pack.Len()))
		zw := zlib.NewWriter(&pack)
		if _, err := zw.Write(obj); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		objs = append(objs, obj)
	}
	p := filepath.Join(t.TempDir(), "objects.pack")
	if err := os.WriteFile(p, pack.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	read := func(i int) {
		got, err := inflateAt(f, offs[i%len(objs)], uint64(len(objs[i%len(objs)])))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, objs[i%len(objs)]) {
			t.Fatalf("object %d inflated to %q", i%len(objs), got)
		}
	}
	read(0) // the first read makes the reader the rest reuse
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range reads {
		read(i)
	}
	runtime.ReadMemStats(&after)
	if raceDetector {
		return // the detector drops pooled readers at random (race_on_test.go); every object still inflated right above
	}
	if per := (after.TotalAlloc - before.TotalAlloc) / reads; per > 4<<10 {
		t.Fatalf("each object read allocated %d bytes, want under 4 KB (a new zlib reader is ~40 KB)", per)
	}
}

// TestObjectCacheBoundedInUse (bug hunt H-277, devco:D-1015): the object
// cache of a store opened for one command keeps at most
// objectCacheMaxBytes, as a kept store does between commands; past it it
// starts over, and every object still reads as git has it. Before, a
// command that walked a big store's trees kept every tree and delta base
// it read for the rest of the run. Not parallel: it lowers the bound.
func TestObjectCacheBoundedInUse(t *testing.T) {
	work, _, _ := fixture(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	var ids []string
	for _, rev := range []string{"HEAD", "HEAD~1", "HEAD~2", "HEAD~3"} {
		ids = append(ids, gitT(t, work, "rev-parse", rev+":org/tasks/t1.json"), gitT(t, work, "rev-parse", rev+"^{tree}"))
	}
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
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) < 4 {
		t.Fatalf("the fixture has %d distinct objects, want at least 4", len(ids))
	}
	first, err := objs.read(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	prev := objectCacheMaxBytes
	objectCacheMaxBytes = len(first.data) + 1 // room for one more small object at most
	t.Cleanup(func() { objectCacheMaxBytes = prev })
	for round := range 2 {
		for _, id := range ids {
			o, err := objs.read(id)
			if err != nil {
				t.Fatal(err)
			}
			want := gitT(t, work, "cat-file", "-p", id)
			if o.typ == typeBlob && strings.TrimSpace(string(o.data)) != want {
				t.Fatalf("round %d: %s reads %q, git has %q", round, id, o.data, want)
			}
			objs.mu.Lock()
			held := objs.bytes
			objs.mu.Unlock()
			if held > objectCacheMaxBytes {
				t.Fatalf("round %d: the store keeps %d bytes, past its bound of %d", round, held, objectCacheMaxBytes)
			}
		}
	}
}
