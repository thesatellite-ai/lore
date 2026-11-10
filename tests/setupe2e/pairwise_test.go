//go:build setupe2e

package setupe2e

import (
	"fmt"
	"slices"
	"testing"
)

// TestPairwiseCoversEveryPair: every value pair any valid shape holds is
// in the subset, the subset is valid, deterministic and much smaller.
func TestPairwiseCoversEveryPair(t *testing.T) {
	sub := PairwiseShapes()
	covered := map[pairKey]bool{}
	for _, s := range sub {
		if err := s.Validate(); err != nil {
			t.Fatalf("pairwise shape %s invalid: %v", s.Name(), err)
		}
		for _, p := range shapePairs(s) {
			covered[p] = true
		}
	}
	all := AllShapes()
	want := 0
	seen := map[pairKey]bool{}
	for _, s := range all {
		for _, p := range shapePairs(s) {
			if !seen[p] {
				seen[p] = true
				want++
			}
			if !covered[p] {
				t.Errorf("pair %+v (e.g. in %s) not covered", p, s.Name())
			}
		}
	}
	if !slices.Equal(sub, PairwiseShapes()) {
		t.Errorf("PairwiseShapes is not deterministic")
	}
	if len(sub) >= len(all)/10 {
		t.Errorf("subset has %d of %d shapes; expected far fewer", len(sub), len(all))
	}
	t.Logf("%d shapes cover all %d pairs (full product: %d shapes)", len(sub), want, len(all))
}

// TestShapeValidate pins the invalid pairs and the parser's refusals.
func TestShapeValidate(t *testing.T) {
	good := `{"gh_accounts":2,"gh_active":"second","ssh":"several","keys":"with_pub","include_if":1,"lore_home":"setups","start":"plain","push":"batch"}`
	if _, err := ParseShape(good); err != nil {
		t.Fatalf("good shape refused: %v", err)
	}
	for name, raw := range map[string]string{
		"second with one account": `{"gh_accounts":1,"gh_active":"second","ssh":"several","keys":"with_pub","include_if":1,"lore_home":"setups","start":"plain","push":"batch"}`,
		"ssh none without pub":    `{"gh_accounts":1,"gh_active":"first","ssh":"none","keys":"without_pub","include_if":1,"lore_home":"setups","start":"plain","push":"batch"}`,
		"missing field":           `{"gh_accounts":1,"gh_active":"first","ssh":"none","keys":"with_pub","include_if":1,"lore_home":"setups","push":"now"}`,
		"unknown field":           `{"gh_accounts":1,"gh_active":"first","ssh":"none","keys":"with_pub","include_if":1,"lore_home":"setups","start":"plain","x":1}`,
		"bad value":               `{"gh_accounts":3,"gh_active":"first","ssh":"none","keys":"with_pub","include_if":1,"lore_home":"setups","start":"plain","push":"batch"}`,
	} {
		if _, err := ParseShape(raw); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if s, err := ParseShape(`{"gh_accounts":1,"gh_active":"first","ssh":"none","keys":"with_pub","include_if":1,"lore_home":"setups","start":"plain"}`); err != nil || s.PushMode() != PushNow {
		t.Errorf("shape without push = %+v, %v; want push now", s, err)
	}
	if _, err := ParseShape(`{"gh_accounts":1,"gh_active":"first","ssh":"none","keys":"with_pub","include_if":1,"lore_home":"setups","start":"plain","push":"hourly"}`); err == nil {
		t.Errorf("push hourly: accepted")
	}
	pushMeetsEveryValue(t)
	if n := len(AllShapes()); n != 1764 {
		t.Errorf("AllShapes = %d, want 1764 (3x2x4x2x3x7x3 minus invalid pairs; push rotates, it does not multiply)", n)
	}
}

// pushMeetsEveryValue (part of TestShapeValidate): the push rotation of AllShapes puts every push
// value beside every value of every other dimension, so the API matrix
// (and through the pairs, the page matrix) saves the Stores step with
// each mode on every kind of machine.
func pushMeetsEveryValue(t *testing.T) {
	t.Helper()
	seen := map[[3]string]bool{}
	counts := map[string]int{}
	for _, s := range AllShapes() {
		v := dimValues(s)
		counts[s.Push]++
		for i := 0; i < len(v)-1; i++ {
			seen[[3]string{fmt.Sprint(i), v[i], s.Push}] = true
		}
	}
	for _, s := range AllShapes() {
		v := dimValues(s)
		for i := 0; i < len(v)-1; i++ {
			for _, p := range PushValues {
				if !seen[[3]string{fmt.Sprint(i), v[i], p}] {
					t.Errorf("push %s never meets dimension %d value %s", p, i, v[i])
				}
			}
		}
	}
	for _, p := range PushValues {
		if counts[p] < len(AllShapes())/4 {
			t.Errorf("push %s in only %d of %d shapes", p, counts[p], len(AllShapes()))
		}
	}
}
