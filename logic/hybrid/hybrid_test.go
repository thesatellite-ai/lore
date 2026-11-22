package hybrid

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/embed"
)

// TestTermsReadLikeTheIndex: the terms are what lore's FTS5 index stores
// (porter over unicode61, diacritics removed, dotless i as i, emoji kept).
func TestTermsReadLikeTheIndex(t *testing.T) {
	got := Terms(nil, "Running deployments, CAFÉ résumé; ıspanak 🚀 v2 go")
	want := []string{"run", "deploy", "cafe", "resum", "ispanak", "🚀", "v2", "go"}
	if !slices.Equal(got, want) {
		t.Fatalf("terms %q, want %q", got, want)
	}
}

// TestBM25WeighsRareWords: a text holding the rare word outranks one
// holding only the common word many times (word counting ranked them
// alike: one word each).
func TestBM25WeighsRareWords(t *testing.T) {
	ix := Build([]Doc{
		{Text: "the deploy notes for the deploy of the deploy"},
		{Text: "the orbit project budget"},
		{Text: "deploy checklist"},
		{Text: "deploy runbook"},
	})
	s := ix.BM25([]string{"orbit", "deploy"})
	if !(s[1] > s[0] && s[1] > s[2]) {
		t.Fatalf("bm25 %v: the text with the rare word must rank first", s)
	}
}

// TestFuseIsWsum50: each source divided by its best score, half each,
// only for texts in a source's first pool places.
func TestFuseIsWsum50(t *testing.T) {
	a := []float64{4, 2, 0, 1}
	c := []float64{0.2, 0.4, 0.1, -0.3}
	got := Fuse(a, c, 2)
	want := []float64{0.5*1 + 0.5*0.5, 0.5*0.5 + 0.5*1, 0, 0}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-12 {
			t.Fatalf("fused %v, want %v", got, want)
		}
	}
}

// TestFusedFindsAParaphrase: a question sharing no word with the text
// that answers it still ranks that text first once the vectors join the
// words (BM25 alone gives it 0).
func TestFusedFindsAParaphrase(t *testing.T) {
	m, err := embed.Default()
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{
		"Dana relocated to Lisbon last spring and rents a flat near the river",
		"The weekly sync moved to Thursdays",
		"Invoice numbering restarts every January",
		"Lunch order: two salads and a soup",
	}
	docs := make([]Doc, len(texts))
	for i, s := range texts {
		docs[i] = Doc{Text: s, Vec: m.Embed(s)}
	}
	ix := Build(docs)
	q := "which city is dana's home now"
	if bm := ix.BM25([]string{"which", "city", "dana", "home", "now"}); bm[0] == 0 {
		t.Fatalf("fixture: the answer must share the name with the question for BM25 (%v)", bm)
	}
	f := ix.Fused([]string{"city", "home"}, m.Embed(q))
	best := 0
	for i := range f {
		if f[i] > f[best] {
			best = i
		}
	}
	if best != 0 {
		t.Fatalf("fused %v: want the relocation text first", f)
	}
}

// TestKeptTermsBuildTheSameIndex: an index built from terms the caller
// kept (devco D-902) scores as one built from the texts, and building
// does not write over a kept terms slice.
func TestKeptTermsBuildTheSameIndex(t *testing.T) {
	texts := []string{"Parking at the cabin: the north lot", "Code freeze starts Monday", "Parking code 4417 for the gate"}
	plain := make([]Doc, len(texts))
	kept := make([]Doc, len(texts))
	var saved [][]string
	for i, s := range texts {
		plain[i] = Doc{Text: s}
		ts := Terms(nil, s)
		saved = append(saved, append([]string(nil), ts...))
		kept[i] = Doc{Text: s, Terms: ts}
	}
	kept[1].Terms = nil // a mix: one doc read from its text
	a, b := Build(plain).BM25([]string{"parking", "code"}), Build(kept).BM25([]string{"parking", "code"})
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("scores differ at %d: %v vs %v", i, a, b)
		}
	}
	for i, d := range kept {
		if d.Terms != nil && strings.Join(d.Terms, " ") != strings.Join(saved[i], " ") {
			t.Fatalf("doc %d's kept terms were written over: %v", i, d.Terms)
		}
	}
}
