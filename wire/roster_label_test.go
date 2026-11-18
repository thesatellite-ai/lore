package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestRosterLabelD647: devco D-647, `team list --setup <ops setup>` said
// "no teams on acmeops org/'s roster": the roster ops name is the org
// entry and the store folder holding it, "acmeops (acmeops/org)"; the
// repo's own roster keeps its name.
func TestRosterLabelD647(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		at   rosterAt
		want string
	}{
		{newRosterAt("acmeops/org", "acmeops org/"), "acmeops (acmeops/org)"},
		{newRosterAt(store.SourceRepo, repoRosterWhere), repoRosterWhere},
		{newRosterAt("agency-notes", "agency org/"), "agency org/"},
	} {
		if got := c.at.label(); got != c.want {
			t.Errorf("label(%s, %q) = %q, want %q", c.at.src, c.at.where, got, c.want)
		}
		if got := (&profileRoster{at: c.at}).Label(); got != c.want {
			t.Errorf("Label() = %q, want %q", got, c.want)
		}
		if got := (&profileRoster{at: c.at}).Where(); got != c.at.where {
			t.Errorf("Where() = %q, want %q unchanged", got, c.at.where)
		}
	}
}
