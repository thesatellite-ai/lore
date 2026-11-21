package settings_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResShowWithRefsRoutesTheWholeStore (devco D-792): resource show
// --with-refs --store me/work opens every folder of me, so the rows of
// me/journal that reference a person kept in me/work are listed; without
// --with-refs, or with a store and no folder, the read stays as named.
func TestResShowWithRefsRoutesTheWholeStore(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		store    string
		withRefs bool
		want     string
	}{
		{"me/work", true, "me"},
		{"me/work", false, "me/work"},
		{"me", true, "me"},
		{"", true, ""},
		{"repo", true, "repo"},
	} {
		in := settings.ResShowIn{StoreFlag: op.StoreFlag{Store: c.store}, WithRefs: c.withRefs}
		if got := in.RoutedStore(); got != c.want {
			t.Errorf("--store %q with_refs=%v routes to %q, want %q", c.store, c.withRefs, got, c.want)
		}
	}
}
