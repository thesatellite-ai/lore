package searchrank

import (
	"math"
	"testing"
)

// TestShareRarestWordIsOne (devco:D-1007): the word fewest rows hold
// counts 1, a word many more rows hold far less, and words held equally
// often all count 1.
func TestShareRarestWordIsOne(t *testing.T) {
	s := Share([]int{1, 40, 0})
	if s[0] != 1 || s[2] != 1 {
		t.Fatalf("rarest words %v, want 1 (a word nothing holds is rarest)", s)
	}
	if s[1] >= 0.3 {
		t.Errorf("a word 40 rows hold counts %.2f, want far less than the rare one", s[1])
	}
	for _, v := range Share([]int{7, 7}) {
		if v != 1 {
			t.Errorf("equal counts give %v, want every word 1", v)
		}
	}
	if Share(nil) != nil {
		t.Error("no words, want no shares")
	}
}

// TestWeightCountsNameWordsInFull (devco:D-1007): a word that counts in
// full weighs 1, one that counts by rarity its share; the weight is the
// average over the held words only.
func TestWeightCountsNameWordsInFull(t *testing.T) {
	share := []float64{1, 0.2, 0.2}
	held := []bool{true, true, false}
	if got := Weight(held, []bool{true, true, true}, share); got != 1 {
		t.Errorf("every word in full: %v, want 1", got)
	}
	if got := Weight(held, []bool{false, false, false}, share); math.Abs(got-0.6) > 1e-9 {
		t.Errorf("by rarity: %v, want 0.6 (1 and 0.2 over two held words)", got)
	}
	if got := Weight([]bool{false, false, false}, make([]bool, 3), share); got != 1 {
		t.Errorf("nothing held: %v, want 1", got)
	}
	if got := Weight(held, nil, share); got != 1 {
		t.Errorf("slices that do not line up: %v, want 1", got)
	}
}

// TestWeightedKeepsTheBonus: only the matched part is scaled.
func TestWeightedKeepsTheBonus(t *testing.T) {
	s := Score(2, 3, false, 0, 0) // (2 + 0.25) / 4
	if got := Weighted(s, 2, 3, 1); got != s {
		t.Errorf("weight 1: %v, want %v", got, s)
	}
	if got, want := Weighted(s, 2, 3, 0.5), (1+0.25)/4.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("weight 0.5: %v, want %v", got, want)
	}
}
