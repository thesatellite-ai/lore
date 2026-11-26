package gitfast

import (
	"strconv"
	"strings"
	"testing"
)

// TestBigPackIndexIsReadOnDisk (bug hunt H-279, devco:D-1017): a pack
// index bigger than idxInMemoryMax is never loaded whole (every command
// that read one object of a 300k-commit store held its 45 MB index); its
// ids are looked up in the file, and every object of the pack reads as
// git has it, while an id the pack lacks is missing. Not parallel: it
// lowers the bound.
func TestBigPackIndexIsReadOnDisk(t *testing.T) {
	work, _, _ := fixture(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	gitT(t, work, "repack", "-a", "-d", "-q")
	prev := idxInMemoryMax
	idxInMemoryMax = 0 // every index counts as big
	t.Cleanup(func() { idxInMemoryMax = prev })
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
	listed := strings.Fields(gitT(t, work, "cat-file", "--batch-all-objects", "--batch-check=%(objectname) %(objecttype) %(objectsize)"))
	if len(listed) < 3*10 {
		t.Fatalf("the fixture packs %d objects, want at least 10", len(listed)/3)
	}
	for i := 0; i+2 < len(listed); i += 3 {
		id, typ, size := listed[i], listed[i+1], listed[i+2]
		o, err := objs.read(id)
		if err != nil {
			t.Fatalf("%s %s: %v", typ, id, err)
		}
		if o.typ != typ || strconv.Itoa(len(o.data)) != size {
			t.Fatalf("%s reads as a %s of %d bytes, git has a %s of %s", id, o.typ, len(o.data), typ, size)
		}
	}
	objs.mu.Lock()
	for _, p := range objs.packs {
		if p.idx == nil || p.ids != nil || p.offsets != nil {
			t.Errorf("pack %s: its index was loaded into memory, want it read in its file", p.path)
		}
	}
	objs.mu.Unlock()
	if _, err := objs.read(strings.Repeat("f", 40)); err == nil {
		t.Error("an id the pack lacks was found")
	}
}
