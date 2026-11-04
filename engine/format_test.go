package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// gatedBackend is a backend whose folder declares d.
type gatedBackend struct {
	store.Backend
	d formatgate.Declared
}

func (g gatedBackend) Format(context.Context) (formatgate.Declared, error) { return g.d, nil }

// TestGateRefusesUnknownFeature: a store declaring a feature this lore
// does not know refuses every write with E_OLD_LORE and writes nothing; a
// known declaration writes.
func TestGateRefusesUnknownFeature(t *testing.T) {
	for name, c := range map[string]struct {
		d     formatgate.Declared
		write bool
	}{
		"unknown": {formatgate.Declared{Features: []string{"live", "zz_future"}}, false},
		"known":   {formatgate.Declared{Features: []string{"hlc"}, MinLoreVersion: "0.1.18"}, true},
		"floor":   {formatgate.Declared{MinLoreVersion: "9.0.0"}, false}, // above every release this lore knows
	} {
		data := memstore.NewData()
		f := setup(t, WithStore(Attached{Name: "gated", Backend: gatedBackend{Backend: data.Open(store.OpenOptions{}), d: c.d}}))
		_, err := f.eng.Create(context.Background(), "gated", schema.TableProjects, map[string]value.Value{"name": txt("x")})
		if c.write != (err == nil) || (!c.write && !errs.Has(err, errs.OldLore)) {
			t.Fatalf("%s: err %v", name, err)
		}
		rows, _, _ := data.Open(store.OpenOptions{}).Read(context.Background(), "")
		if !c.write && len(rows) != 0 {
			t.Fatalf("%s: a refused write wrote %d rows", name, len(rows))
		}
	}
}
