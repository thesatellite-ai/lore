package e2eenv

import (
	"slices"
	"testing"
)

func TestLoreGoEnv(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     []string
	}{
		{"unset", "", nil},
		{"runtime settings", "GOMAXPROCS=2 GOGC=off GOMEMLIMIT=512MiB", []string{"GOMAXPROCS=2", "GOGC=off", "GOMEMLIMIT=512MiB"}},
		{"extra spaces", "  GOGC=400   GOMAXPROCS=4 ", []string{"GOGC=400", "GOMAXPROCS=4"}},
		// Only cost settings pass: a variable that could change what lore does is dropped.
		{"not a runtime setting", "HOME=/x LORE_ACTOR=bob GOGC=200", []string{"GOGC=200"}},
		{"no value or no =", "GOGC= GOMAXPROCS", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(Var, tc.in)
			if got := LoreGoEnv(); !slices.Equal(got, tc.want) {
				t.Fatalf("LoreGoEnv(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
