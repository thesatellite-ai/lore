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
