package merge

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestReservedClashIsReported: an underscore key this lore does not model
// (a future "_w") that both sides set differently is settled by the newer
// side, like an undeclared field, and listed in NewerWins so the merge
// driver's stderr names it (the older value never vanishes silently); a
// one-sided change, an agreed value and a removal by one side are not
// clashes.
func TestReservedClashIsReported(t *testing.T) {
	with := func(r row.Row, v string) row.Row {
		if v != "" {
			r.Reserved = map[string]value.Value{"_w": text(v)}
		}
		return r
	}
	cases := []struct {
		name             string
		base, ours, thrs string
		want             string // merged "_w", "" = absent
		reported         bool
	}{
		{"both changed, theirs newer", "a", "b", "c", "c", true},
		{"both changed, one removed it", "a", "", "c", "c", true},
		{"only theirs changed", "a", "a", "c", "c", false},
		{"both set the same", "a", "c", "c", "c", false},
		{"theirs removed it", "a", "a", "", "", false},
		{"only ours added it", "", "b", "", "b", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := with(live(stamp(1, actorA), text("body"), text("x")), c.base)
			ours := with(live(stamp(2, actorA), text("body"), text("x")), c.ours)
			theirs := with(live(stamp(3, actorB), text("body"), text("x")), c.thrs)
			res, err := Rows(PolicyFor(testDef), base, ours, theirs)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if v, ok := res.Row.Reserved["_w"]; ok {
				got = v.String()
			}
			if got != c.want {
				t.Errorf("_w = %q, want %q", got, c.want)
			}
			if r := slices.Contains(res.NewerWins, "_w"); r != c.reported {
				t.Errorf("NewerWins %v: reported=%v, want %v", res.NewerWins, r, c.reported)
			}
			if !res.Clean() {
				t.Errorf("a reserved key clash is never a conflict: %+v", res.Conflicts)
			}
		})
	}
}
