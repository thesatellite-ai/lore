package merge

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestRebuiltReplacedWhole: a real key row replaces a newer rebuilt guess
// whole, flags included, on either side (METADATA 3.18 step 6); two real
// rows still merge field by field.
func TestRebuiltReplacedWhole(t *testing.T) {
	kid := mustAny("mky_01936f3300007d4e8f5a6b7c8d9e0f1a")
	key := func(ms int, rebuilt, internal bool) row.Row {
		r := row.Row{Table: schema.TableMetaKeys, ID: kid, HLC: stamp(ms, actorA), CreatedAt: t0, CreatedBy: actorA,
			Fields: map[string]value.Value{"name": text("margin_pct"), "internal": value.BoolOf(internal)}}
		if rebuilt {
			r.Fields["rebuilt"] = value.BoolOf(true)
		}
		return r
	}
	def := schema.Table(schema.TableMetaKeys, kid.Prefix(), schema.Text("name"), schema.Bool("internal"), schema.Bool("rebuilt"))
	real, guess := key(1, false, false), key(5, true, true) // the guess is newer
	for _, sides := range [][2]row.Row{{real, guess}, {guess, real}} {
		res, err := Rows(PolicyFor(def), Absent, sides[0], sides[1])
		if err != nil {
			t.Fatal(err)
		}
		if field(res.Row, "internal") != "false" || field(res.Row, "rebuilt") != "<absent>" {
			t.Fatalf("real row must win whole: internal=%s rebuilt=%s", field(res.Row, "internal"), field(res.Row, "rebuilt"))
		}
	}
	if _, ok := replaceRebuilt(key(1, false, false), key(2, false, true)); ok {
		t.Fatal("two real rows must merge normally")
	}
	if _, ok := replaceRebuilt(key(1, true, false), key(2, true, true)); ok {
		t.Fatal("two rebuilt rows must merge normally")
	}
}
