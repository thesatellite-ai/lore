package filter

import (
	"slices"
	"testing"
)

// TestValueWithSpaces: a word that starts no term continues the value
// before it (spaces kept), so `title~=bulk plan` is one term; words that
// start terms still split; a stray word with nothing to continue, or after
// a term that takes no value or a quoted one, is still refused.
func TestValueWithSpaces(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want [][]string // values per term
	}{
		{"title~=bulk plan", [][]string{{"bulk plan"}}},
		{"title~=bulk  plan  v2", [][]string{{"bulk  plan  v2"}}},
		{"priority=high status=open", [][]string{{"high"}, {"open"}}},
		{"title~=bulk plan priority=high", [][]string{{"bulk plan"}, {"high"}}},
		{`title="a b" priority=low`, [][]string{{"a b"}, {"low"}}},
	} {
		f, err := Parse([]string{tc.in})
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		var got [][]string
		for _, term := range f.Terms {
			got = append(got, term.Values)
		}
		if !slices.EqualFunc(got, tc.want, func(a, b []string) bool { return slices.Equal(a, b) }) {
			t.Errorf("%q: values %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"plan", "due? plan", `title="a b" plan`} {
		if _, err := Parse([]string{bad}); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
