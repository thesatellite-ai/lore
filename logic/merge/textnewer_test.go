package merge

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTextNewerWinsNeverMarks (devco D-219, L-19): two people retitled one
// live task at once; the live merge wrote conflict markers into title and
// body and pushed them to the shared live ref, so every clone listed a
// five-line title and the sync of people who never touched the row failed
// E_STALE. With TextNewerWins the newer edit (by HLC) is kept whichever
// side is "ours", the row is clean, and every replaced value is named.
func TestTextNewerWinsNeverMarks(t *testing.T) {
	def := schema.Table(schema.TableMemories, ids.PrefixMemory,
		schema.Text("title"),
		schema.Text("body"),
		schema.Text("priority", schema.OneOf("low", "high", "low2")),
	)
	p := PolicyFor(def)
	p.TextNewerWins = true
	base := live(stamp(0, actorA), text("title"), text("t"), text("body"), text("b"), text("priority"), text("low"))
	mei := live(stamp(5, actorA), text("title"), text("t (Mei)"), text("body"), text("b Mei"), text("priority"), text("high"))
	tom := live(stamp(9, actorB), text("title"), text("t (Tom)"), text("body"), text("b Tom"), text("priority"), text("low2"))
	for _, swap := range []bool{false, true} {
		o, th := mei, tom
		if swap {
			o, th = tom, mei
		}
		res, err := Rows(p, base, o, th)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Clean() {
			t.Fatalf("swap=%v: conflicts %v, want none", swap, res.Conflicts)
		}
		for f, want := range map[string]string{"title": "t (Tom)", "body": "b Tom", "priority": "low2"} {
			if got := field(res.Row, f); got != want || strings.Contains(got, MarkerOpen) {
				t.Fatalf("swap=%v: %s = %q, want the newer %q and no markers", swap, f, got, want)
			}
		}
		var got []string
		for _, r := range res.Replaced {
			got = append(got, r.Field+"="+r.Older.String())
		}
		if strings.Join(got, ",") != "body=b Mei,priority=high,title=t (Mei)" {
			t.Fatalf("swap=%v: replaced %v, want every older value named", swap, got)
		}
	}
	// Without the switch a text clash still keeps both in markers (the git
	// merge driver and store sync rely on it).
	p.TextNewerWins = false
	res, err := Rows(p, base, mei, tom)
	if err != nil {
		t.Fatal(err)
	}
	if res.Clean() || !strings.Contains(field(res.Row, "title"), MarkerOpen) {
		t.Fatalf("default policy: title %q, want markers", field(res.Row, "title"))
	}
}
