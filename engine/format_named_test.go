package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// TestFormatGateNamedStore: FormatGate answers the same as a write into
// the named store (unknown feature or floor above: E_OLD_LORE; a known
// declaration or none: nil), and an unknown store name is not gated.
func TestFormatGateNamedStore(t *testing.T) {
	for name, c := range map[string]struct {
		d       formatgate.Declared
		refused bool
	}{
		"nothing declared": {formatgate.Declared{}, false},
		"known":            {formatgate.Declared{Features: []string{"hlc"}}, false},
		"unknown feature":  {formatgate.Declared{Features: []string{"zz_future"}}, true},
		"floor above":      {formatgate.Declared{MinLoreVersion: "9.0.0"}, true},
	} {
		data := memstore.NewData()
		f := setup(t, WithStore(Attached{Name: "gated", Backend: gatedBackend{Backend: data.Open(store.OpenOptions{}), d: c.d}}))
		err := f.eng.FormatGate(context.Background(), "gated")
		if c.refused != (err != nil) || (c.refused && !errs.Has(err, errs.OldLore)) {
			t.Fatalf("%s: err %v", name, err)
		}
		if err := f.eng.FormatGate(context.Background(), "not_attached"); err != nil {
			t.Fatalf("%s: an unknown store is not gated: %v", name, err)
		}
	}
}
