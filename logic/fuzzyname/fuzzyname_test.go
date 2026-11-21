package fuzzyname

import (
	"slices"
	"testing"
)

// TestNear: a word a letter or two off a name or alias finds it through
// the trigram index; only the closest terms are returned; a word with no
// shared trigram is never compared (no full scan).
func TestNear(t *testing.T) {
	t.Parallel()
	x := New()
	for i, term := range []string{"robert-kim", "Bobby", "alice-ng", "alix-ng", "lumen", "Robert Kim"} {
		x.Add(term, i)
	}
	terms := func(hs []Hit) []string {
		var out []string
		for _, h := range hs {
			out = append(out, h.Term)
		}
		return out
	}
	cases := []struct {
		word   string
		budget int
		want   []string
	}{
		{"boby", 1, []string{"bobby"}},
		{"robret-kim", 2, []string{"robert-kim"}},
		{"alise-ng", 1, []string{"alice-ng"}},
		{"lumne", 1, []string{"lumen"}},
		{"robert kin", 1, []string{"robert kim"}},
		{"BOBBY", 1, []string{"bobby"}},
		{"zzzz", 2, nil},
	}
	for _, c := range cases {
		if got := terms(x.Near(c.word, c.budget)); !slices.Equal(got, c.want) {
			t.Errorf("Near(%q, %d) = %v, want %v", c.word, c.budget, got, c.want)
		}
	}
	// closest only: alice-ng is 0 off, alix-ng 2 off
	if got := terms(x.Near("alice-ng", 2)); !slices.Equal(got, []string{"alice-ng"}) {
		t.Errorf("closest only: %v", got)
	}
	if h := x.Near("bobby", 1); len(h) != 1 || h[0].Owner != 1 || h[0].Dist != 0 {
		t.Errorf("owner/dist: %+v", h)
	}
}

// TestOSA: a swap of two neighbours is one edit; a length gap over the
// limit is limit+1 at once.
func TestOSA(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b  string
		limit int
		want  int
	}{{"lumne", "lumen", 2, 1}, {"alise-ng", "alice-ng", 2, 1}, {"orbit", "orbit", 2, 0}, {"money", "onyx", 2, 3}, {"ab", "abcdef", 2, 3}} {
		if got := OSA(c.a, c.b, c.limit); got != c.want {
			t.Errorf("OSA(%q, %q, %d) = %d, want %d", c.a, c.b, c.limit, got, c.want)
		}
	}
}

// TestBudget: no typo under four letters, one up to seven, two from eight.
func TestBudget(t *testing.T) {
	t.Parallel()
	for w, want := range map[string]int{"cfo": 0, "boby": 1, "robertk": 1, "robert-kim": 2} {
		if got := Budget(w); got != want {
			t.Errorf("Budget(%q) = %d, want %d", w, got, want)
		}
	}
}
