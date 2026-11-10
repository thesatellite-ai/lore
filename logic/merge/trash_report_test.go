package merge

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
)

// TestTrashOutcomeIsTheOneStatement (TE-F-3): once a soft-delete rule
// settled the delete fields, the report never also lists them as "both
// sides changed …; kept the newer edit" (two deletes keep the EARLIER one,
// so that line contradicted the outcome). A real clash on another field of
// the same row is still reported. Both argument orders.
func TestTrashOutcomeIsTheOneStatement(t *testing.T) {
	p := PolicyFor(testDef)
	base := live(stamp(0, actorA), text("body"), text("v1"), text("kind"), text("core"))
	del := trashed(t, base, stamp(10, actorA), 10, actorA, "duplicate", batA)
	delLater := trashed(t, base, stamp(30, actorB), 30, actorB, "stale", batB)
	restored := live(stamp(40, actorA), text("body"), text("v1"), text("kind"), text("core"))

	kindA := live(stamp(5, actorA), text("body"), text("v1"), text("kind"), text("episodic"))
	kindB := live(stamp(6, actorB), text("body"), text("v1"), text("kind"), text("procedural"))
	delKindA := trashed(t, kindA, stamp(10, actorA), 10, actorA, "duplicate", batA)
	delKindB := trashed(t, kindB, stamp(30, actorB), 30, actorB, "stale", batB)

	cases := []struct {
		name              string
		base, ours, their row.Row
		outcome           TrashOutcome
		newerWins         []string
	}{
		{"deleted on both sides", base, del, delLater, TrashDeletedBoth, nil},
		{"restored vs deleted again", del, restored, delLater, TrashKeptRestore, nil},
		{"deleted on both sides, kind changed on both", base, delKindA, delKindB, TrashDeletedBoth, []string{"kind"}},
	}
	for _, c := range cases {
		for _, swap := range []bool{false, true} {
			o, th := c.ours, c.their
			if swap {
				o, th = th, o
			}
			res, err := Rows(p, c.base, o, th)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if res.Trash != c.outcome {
				t.Errorf("%s (swap %v): outcome %q, want %q", c.name, swap, res.Trash, c.outcome)
			}
			if !slices.Equal(res.NewerWins, c.newerWins) {
				t.Errorf("%s (swap %v): NewerWins %q, want %q", c.name, swap, res.NewerWins, c.newerWins)
			}
		}
	}
}
