package wire

import "testing"

// TestLeftoverWhy (devco R-7 leftover): a clone no list of the machine
// names is a leftover when it is no lore store or sits outside every org
// store's host and owner; a known store, or an unknown store under the
// org's own owner (maybe attached by hand), is never named.
func TestLeftoverWhy(t *testing.T) {
	t.Parallel()
	known := []string{"git@git.example.test:acme/team-store.git"}
	orgs := []string{"git.example.test/acme/team-store"}
	cases := map[string]struct {
		url    string
		marker bool
		named  bool
	}{
		"known store":             {"git@git.example.test:acme/team-store.git", true, false},
		"outside host":            {"git@git.other.test:other/notes.git", true, true},
		"not a store":             {"git@git.example.test:acme/personal-x.git", false, true},
		"store under org's owner": {"git@git.example.test:acme/second-store.git", true, false},
	}
	for name, c := range cases {
		if got := leftoverWhy(known, orgs, c.url, c.marker); (got != "") != c.named {
			t.Errorf("%s: leftoverWhy = %q, want named %v", name, got, c.named)
		}
	}
}
