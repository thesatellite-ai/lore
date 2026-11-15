package merge

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// TestClashesAndNewerText (devco V-12, V-15): Clashes names the text
// fields both sides changed (what a landing refusal names); MergeNewerText
// keeps the newer text of such a field and every field one side alone
// changed, never markers (the D-219 rule two copies of one branch layer
// merge with), and reports the value it replaced.
func TestClashesAndNewerText(t *testing.T) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	base := live(stamp(0, actorA), text("body"), text("start"), text("kind"), text("core"))
	ours := live(stamp(5, actorA), text("body"), text("MINE"), text("kind"), text("core"))
	theirs := live(stamp(6, actorB), text("body"), text("THEIRS"), text("kind"), text("episodic"))
	var reported Result
	d := Driver{Registry: reg, Report: func(r Result) { reported = r }}
	fields, err := d.Clashes(encode(t, base), encode(t, ours), encode(t, theirs))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fields, []string{"body"}) {
		t.Fatalf("Clashes = %v, want [body]", fields)
	}
	out, err := d.MergeNewerText(encode(t, base), encode(t, ours), encode(t, theirs))
	if err != nil {
		t.Fatal(err)
	}
	got, err := row.Decode(out)
	if err != nil {
		t.Fatal(err)
	}
	if field(got, "body") != "THEIRS" || field(got, "kind") != "episodic" {
		t.Fatalf("body %q kind %q; want the newer body and theirs' kind", field(got, "body"), field(got, "kind"))
	}
	if len(reported.Replaced) != 1 || reported.Replaced[0].Field != "body" || reported.Replaced[0].Older.String() != "MINE" {
		t.Fatalf("Replaced = %+v, want body replacing MINE", reported.Replaced)
	}
}
