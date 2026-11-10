package formatgate

import (
	"strings"
	"testing"
)

// TestReadOnlyLine: no line for a writable project; an unknown feature and
// a floor above this binary each name the cause and the upgrade fix
// (FORMAT_EVOLUTION rule 2; ported from saas/cmd/cli TestReadOnlyLine).
func TestReadOnlyLine(t *testing.T) {
	cases := []struct {
		name string
		d    Declared
		want []string // empty: no line
	}{
		{"writable", Declared{Features: []string{Live}}, nil},
		{"nothing declared", Declared{}, nil},
		{"unknown feature", Declared{Features: []string{"zz_future"}}, []string{"lore: read-only here (", "feature zz_future is unknown", "`lore upgrade`"}},
		{"unknown feature and floor", Declared{Features: []string{"zz_future"}, MinLoreVersion: "9.0.0"}, []string{"zz_future", "needs lore 9.0.0"}},
		{"floor", Declared{MinLoreVersion: "9.0.0"}, []string{"needs lore 9.0.0; this is 0.1.18", "reading works"}},
	}
	for _, c := range cases {
		got := ReadOnlyLine(Check("v0.1.18", c.d), "lore")
		if len(c.want) == 0 {
			if got != "" {
				t.Errorf("%s: %q, want no line", c.name, got)
			}
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", c.name, got, w)
			}
		}
	}
}
