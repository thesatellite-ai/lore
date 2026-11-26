package row

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

// memoDoc is a task row document long enough for the memo.
func memoDoc(t testing.TB) []byte {
	t.Helper()
	doc := []byte(`{"_v":2,"_table":"tasks","id":"tsk_01a1d66426e8785789706d23b90e8c48","_hlc":"2026-11-13T19:00:00.000Z/0000/","title":"memo probe","status":"todo","tags":["a","b"]}`)
	if _, err := decode(doc); err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}
	return doc
}

// TestDecodeMemoGivesOwnMaps: the same bytes decoded twice give equal rows,
// and a caller that edits its row's maps changes neither the memo nor
// another caller's copy (rows are edited in place before writes).
func TestDecodeMemoGivesOwnMaps(t *testing.T) {
	doc := memoDoc(t)
	first, err := Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	edited, _ := value.TextOf("edited")
	first.Fields["title"] = edited
	delete(first.Fields, "status")
	second, err := Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	want, err := decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := Encode(second)
	b, _ := Encode(want)
	if string(a) != string(b) {
		t.Fatalf("memo answer differs from a fresh decode:\n%s\n%s", a, b)
	}
	again, _ := value.TextOf("again")
	second.Fields["title"] = again
	third, _ := Decode(doc)
	if got := third.Fields["title"].String(); got != "memo probe" {
		t.Fatalf("a caller's edit reached the memo: title %q", got)
	}
}

// BenchmarkDecodeSameDoc guards the hot path of a list on a big repo: the
// same row file decoded again (by the folder read, then each List) must
// cost a lookup and three map copies, not a parse.
func BenchmarkDecodeSameDoc(b *testing.B) {
	doc := memoDoc(b)
	for b.Loop() {
		if _, err := Decode(doc); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeNoMemo is the parse the memo saves, for comparison.
func BenchmarkDecodeNoMemo(b *testing.B) {
	doc := memoDoc(b)
	for b.Loop() {
		if _, err := decode(doc); err != nil {
			b.Fatal(err)
		}
	}
}

// TestDecodeMemoBoundCountsDecodedRows (bug hunt H-277, devco:D-1015):
// the memo's bound counts what a kept decode holds, its text in the row's
// values and the decoded structure besides (about twice the document),
// not only the document's bytes. Before, it kept the document as its key
// too and counted only that: bounded at 48 MB of documents it held ~145
// MB, the largest part of lore doctor's memory after the store's rows.
// Not parallel: it fills and clears the process's memo.
func TestDecodeMemoBoundCountsDecodedRows(t *testing.T) {
	const size = 2000
	t.Cleanup(func() {
		memo.mu.Lock()
		clear(memo.rows)
		memo.bytes = 0
		memo.mu.Unlock()
	})
	n := decodeMemoMaxBytes / size // every document fits a bound of document bytes
	for i := range n {
		doc := make([]byte, size)
		binaryPut(doc, i)
		memo.put(doc, Row{})
	}
	memo.mu.Lock()
	kept := len(memo.rows)
	memo.mu.Unlock()
	if held := kept * 2 * size; held > decodeMemoMaxBytes {
		t.Fatalf("the memo keeps %d decodes of %d-byte documents (~%d MB with their rows), past its bound of %d MB", kept, size, held>>20, decodeMemoMaxBytes>>20)
	}
	if kept == 0 {
		t.Fatal("the memo keeps nothing")
	}
}

// binaryPut writes i into the first bytes of doc, so every document differs.
func binaryPut(doc []byte, i int) {
	for j := range 8 {
		doc[j] = byte(i >> (8 * j))
	}
}
