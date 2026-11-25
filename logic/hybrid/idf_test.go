package hybrid

import (
	"math"
	"testing"
)

// TestTermIDFIsBM25Weight (devco D-985, hunt H-244): TermIDF is the
// weight BM25 gives the word's term over the index, so lore ask's excerpt
// reads rarity with the scorer's own function; a word every text holds
// weighs almost nothing and one no text holds 0.
func TestTermIDFIsBM25Weight(t *testing.T) {
	ix := Build([]Doc{{Text: "orbit rollout window"}, {Text: "orbit rollout"}, {Text: "orbit release"}, {Text: "orbit deploys"}})
	if got, want := ix.TermIDF("windows"), IDF(4, 1); math.Abs(got-want) > 1e-12 {
		t.Errorf("TermIDF(windows) = %v, want IDF(4, 1) = %v (the stem window)", got, want)
	}
	if got, want := ix.TermIDF("rollout"), IDF(4, 2); math.Abs(got-want) > 1e-12 {
		t.Errorf("TermIDF(rollout) = %v, want %v", got, want)
	}
	if ix.TermIDF("orbit") >= ix.TermIDF("rollout") || ix.TermIDF("orbit") > 0.2 {
		t.Errorf("a word every text holds weighs %v", ix.TermIDF("orbit"))
	}
	if ix.TermIDF("nowhere") != 0 || IDF(0, 0) != 0 {
		t.Error("a word no text holds weighs more than 0")
	}
	// one text holding only the rare word: its BM25 is that weight times
	// the term frequency part
	if s := ix.BM25([]string{"window"}); s[0] <= 0 || s[1] != 0 {
		t.Errorf("BM25(window) = %v", s)
	}
}
