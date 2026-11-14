package merge

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
)

// TestNewestTextFieldNeverMarks (G-11): two agents' task done set
// tasks.done_by to different actors; as free text the clash was written as
// conflict markers (nested on every further clash) and every sync failed.
// A MergeNewest text field takes the newer side and names it in NewerWins.
func TestNewestTextFieldNeverMarks(t *testing.T) {
	def := schema.Table(schema.TableMemories, ids.PrefixMemory,
		schema.Text("body"),
		schema.Text("done_by", schema.MergeBy(schema.MergeNewest)),
	)
	p := PolicyFor(def)
	if p.IsText("done_by") {
		t.Fatal("a MergeNewest field is not free text")
	}
	base := live(stamp(0, actorA), text("body"), text("x"))
	ours := live(stamp(5, actorA), text("body"), text("x"), text("done_by"), text(actorA.String()))
	theirs := live(stamp(9, actorB), text("body"), text("x"), text("done_by"), text(actorB.String()))
	for _, pair := range [][2]bool{{false, false}, {true, false}} {
		o, th := ours, theirs
		if pair[0] {
			o, th = theirs, ours
		}
		res, err := Rows(p, base, o, th)
		if err != nil {
			t.Fatal(err)
		}
		got := field(res.Row, "done_by")
		if !res.Clean() || strings.Contains(got, MarkerOpen) || got != actorB.String() {
			t.Fatalf("done_by = %q conflicts %v, want the newer side %s and no markers", got, res.Conflicts, actorB)
		}
		if len(res.NewerWins) != 1 || res.NewerWins[0] != "done_by" {
			t.Fatalf("newer wins = %v, want [done_by]", res.NewerWins)
		}
	}
}

// TestTaskIdentityFieldsAreNewest pins which task text fields are one value
// (MergeNewest), so a later schema edit cannot turn them back into prose.
func TestTaskIdentityFieldsAreNewest(t *testing.T) {
	var p Policy
	for _, d := range schema.Builtin() {
		if d.Name == schema.TableTasks {
			p = PolicyFor(d)
		}
	}
	if p.IsText == nil {
		t.Fatal("no tasks table")
	}
	for _, f := range []string{"done_by", "done_commit", "work_branch", "approved_by", "approved_hash", "assigned_team", "idempotency_key", "role"} {
		if p.IsText(f) {
			t.Errorf("tasks.%s merges as free text (conflict markers); want MergeNewest", f)
		}
	}
}
