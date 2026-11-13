package merge

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// TestMergePrefer (bug hunt B2-22): settling a clash with one side changes
// only the clashing field; a field only one side changed keeps that change,
// whichever side is preferred.
func TestMergePrefer(t *testing.T) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	base := live(stamp(0, actorA), text("body"), text("start"), text("kind"), text("core"), text("source_ref"), text("s0"))
	ours := live(stamp(5, actorA), text("body"), text("MINE"), text("kind"), text("core"), text("source_ref"), text("s-ours"))
	theirs := live(stamp(6, actorB), text("body"), text("THEIRS"), text("kind"), text("episodic"), text("source_ref"), text("s0"))
	d := Driver{Registry: reg}
	for _, c := range []struct {
		preferTheirs bool
		body         string
	}{{false, "MINE"}, {true, "THEIRS"}} {
		out, err := d.MergePrefer(encode(t, base), encode(t, ours), encode(t, theirs), c.preferTheirs)
		if err != nil {
			t.Fatal(err)
		}
		got, err := row.Decode(out)
		if err != nil {
			t.Fatal(err)
		}
		if field(got, "body") != c.body || field(got, "kind") != "episodic" || field(got, "source_ref") != "s-ours" {
			t.Fatalf("prefer theirs %v: body %q kind %q source_ref %q; want %q, theirs' kind, ours' summary",
				c.preferTheirs, field(got, "body"), field(got, "kind"), field(got, "source_ref"), c.body)
		}
	}
}
