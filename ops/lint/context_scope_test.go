package lint

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestScopeConflicts: a folder must rule that shares its topic with a wider
// must rule is flagged once; --overrides, an unrelated rule, a sibling
// folder and a should rule are not (CONTEXT_BUDGET 6.2.2, 6.12).
func TestScopeConflicts(t *testing.T) {
	booking, other := id(t, ids.PrefixScope, 1).String(), id(t, ids.PrefixScope, 2).String()
	paths := map[string]string{booking: "src/booking", other: "src/other"}
	rule := func(n byte, sev, body, scp, over string) sourced {
		f := map[string]value.Value{"severity": tx(t, sev), "body": tx(t, body)}
		if scp != "" {
			f[schema.FieldScope] = tx(t, scp)
		}
		if over != "" {
			f[schema.FieldOverrides] = tx(t, over)
		}
		return sourced{src: store.SourceRepo, r: row.Row{ID: id(t, ids.PrefixRule, n), Fields: f}}
	}
	wide := rule(1, "must", "Money amounts are stored as integer cents everywhere.", "", "")
	wideID := wide.r.ID.String()
	rules := []sourced{
		wide,
		rule(2, "must", "Money amounts are stored as decimal minor units here.", booking, ""),
		rule(3, "must", "Money amounts are stored as decimal minor units there.", booking, wideID),
		rule(4, "must", "Booking times are UTC.", booking, ""),
		rule(5, "must", "Money amounts are stored as decimal strings.", other, ""),
		rule(6, "should", "Money amounts are stored as decimal minor units.", booking, ""),
	}
	got := scopeConflicts(rules, paths)
	ids2 := map[string]int{}
	for _, f := range got {
		ids2[f.ID]++
	}
	if ids2[id(t, ids.PrefixRule, 2).String()] != 1 || ids2[id(t, ids.PrefixRule, 5).String()] != 1 || len(got) != 2 {
		t.Fatalf("conflicts = %+v", got)
	}
}
