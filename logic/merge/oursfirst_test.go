package merge

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/conflictpick"
)

// TestOursFirst (bug hunt B2-15): a worktree merge (the git merge driver)
// writes this side's text first and the incoming text second, labelled,
// whichever side is newer, so `lore sync resolve --take other` (the second
// side) is the teammate's text. Equal HLCs (hand edits are not
// re-stamped) keep the same order.
func TestOursFirst(t *testing.T) {
	base := live(stamp(0, actorA), text("body"), text("start"))
	for _, c := range []struct {
		name       string
		oursMS, th int
	}{{"theirs newer", 5, 9}, {"ours newer", 9, 5}, {"same stamp", 0, 0}} {
		ours := live(stamp(c.oursMS, actorA), text("body"), text("MINE"))
		theirs := live(stamp(c.th, actorB), text("body"), text("INCOMING"))
		p := PolicyFor(testDef)
		p.OursFirst = true
		res, err := Rows(p, base, ours, theirs)
		if err != nil || res.Clean() {
			t.Fatalf("%s: clean=%v err=%v", c.name, res.Clean(), err)
		}
		got := field(res.Row, "body")
		if i, j := strings.Index(got, "MINE"), strings.Index(got, "INCOMING"); i < 0 || j < i {
			t.Fatalf("%s: ours is not first: %q", c.name, got)
		}
		if !strings.HasPrefix(got, MarkerOpen+LabelOurs) || !strings.Contains(got, MarkerClose+LabelTheirs) {
			t.Fatalf("%s: the markers do not say whose side is whose: %q", c.name, got)
		}
		if k, o := conflictpick.Text(got, false), conflictpick.Text(got, true); k != "MINE" || o != "INCOMING" {
			t.Fatalf("%s: take kept = %q, other = %q", c.name, k, o)
		}
	}
}
