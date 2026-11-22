package vecfile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestRoundTrip: what Write kept, Read returns, merged with what was
// there; a vector of another width is left out.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "cache", "v.bin")
	if got := Read(p, "m", 3); len(got) != 0 {
		t.Fatalf("missing file read %v", got)
	}
	if err := Write(p, "m", 3, map[uint64][]float32{1: {1, 2, 3}, 9: {1}}); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, "m", 3, map[uint64][]float32{2: {-0.5, 0, 0.25}}); err != nil {
		t.Fatal(err)
	}
	want := map[uint64][]float32{1: {1, 2, 3}, 2: {-0.5, 0, 0.25}}
	if got := Read(p, "m", 3); !reflect.DeepEqual(got, want) {
		t.Fatalf("read %v, want %v", got, want)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the file must be the person's alone: %v %v", fi, err)
	}
}

// TestOtherModelReadsEmpty: a file of another model or width is not read
// (its vectors would be wrong), and the next write replaces it.
func TestOtherModelReadsEmpty(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "v.bin")
	if err := Write(p, "old-model", 2, map[uint64][]float32{1: {1, 2}}); err != nil {
		t.Fatal(err)
	}
	if got := Read(p, "new-model", 2); len(got) != 0 {
		t.Fatalf("another model's file read %v", got)
	}
	if got := Read(p, "old-model", 3); len(got) != 0 {
		t.Fatalf("another width's file read %v", got)
	}
	if err := Write(p, "new-model", 2, map[uint64][]float32{2: {3, 4}}); err != nil {
		t.Fatal(err)
	}
	if got := Read(p, "new-model", 2); !reflect.DeepEqual(got, map[uint64][]float32{2: {3, 4}}) {
		t.Fatalf("read %v", got)
	}
}

// TestDamagedRecordSkipped: a record whose bytes changed and a cut last
// record are skipped; the others are read.
func TestDamagedRecordSkipped(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "v.bin")
	if err := Write(p, "m", 2, map[uint64][]float32{1: {1, 2}, 2: {3, 4}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	h := len(header("m", 2))
	b[h+9] ^= 0xff                          // a vector byte of the first record
	b = append(b, b[h:h+recordLen(2)-3]...) // a cut record at the end
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Read(p, "m", 2); len(got) != 1 {
		t.Fatalf("read %v, want the one undamaged record", got)
	}
}

// TestCapStartsOver: past MaxRecords a write keeps only its own vectors.
func TestCapStartsOver(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "v.bin")
	big := map[uint64][]float32{}
	for i := range MaxRecords {
		big[uint64(i)] = []float32{1}
	}
	if err := Write(p, "m", 1, big); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, "m", 1, map[uint64][]float32{1 << 40: {2}}); err != nil {
		t.Fatal(err)
	}
	if got := Read(p, "m", 1); len(got) != 1 {
		t.Fatalf("%d records after the cap, want 1", len(got))
	}
}

// TestCapKeepsTheSetInUse: a write handed more vectors than MaxRecords
// (a store bigger than the bound) keeps every one, and the next write of
// that set with one row changed keeps the set again, never just the
// change (else every call would embed the whole store again).
func TestCapKeepsTheSetInUse(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "v.bin")
	set := map[uint64][]float32{}
	for i := range MaxRecords + 10 {
		set[uint64(i)] = []float32{1}
	}
	if err := Write(p, "m", 1, set); err != nil {
		t.Fatal(err)
	}
	delete(set, 0)
	set[1<<40] = []float32{2}
	if err := Write(p, "m", 1, set); err != nil {
		t.Fatal(err)
	}
	if got := Read(p, "m", 1); len(got) != len(set) {
		t.Fatalf("%d records, want the %d in use", len(got), len(set))
	}
}
