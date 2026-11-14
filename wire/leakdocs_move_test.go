package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
)

// TestLeakSelfMatchByPath: a row moved into a client repo keeps its id, so
// its own row file matches the internal row of that id; withoutSelfMatches
// drops that self match only for a real row file of the write: a
// .lore/data row file for any action, and on a live write also a live-ref
// row file (<folder>/<table>/<id>.json, the folder one a project may use).
// A file that only looks like one (docs/tasks/<id>.json in a commit), a
// reserved live folder, or a row of another table is never a move, and a
// commit's non-row file never gets the "row in this repo" advice.
func TestLeakSelfMatchByPath(t *testing.T) {
	t.Parallel()
	const id = "tsk_0190aaaa0000700080000000000000a1"
	rows := []leakcheck.IndexRow{{ID: id, Table: "tasks", Label: "store northwind"}}
	cases := []struct {
		name, where, path string
		moved             bool
	}{
		{"branch row, commit", LeakWhereCommit, ".lore/data/tasks/" + id + ".json", true},
		{"monorepo branch row, live write", leakWhereLiveWrite, "web/.lore/data/tasks/" + id + ".json", true},
		{"live root row", leakWhereLiveWrite, "_root/tasks/" + id + ".json", true},
		{"live monorepo row", leakWhereLiveWrite, "apps/web/tasks/" + id + ".json", true},
		{"live path in a commit is no row", LeakWhereCommit, "docs/tasks/" + id + ".json", false},
		{"live path in a push is no row", LeakWherePush, "_root/tasks/" + id + ".json", false},
		{"reserved live folder", leakWhereLiveWrite, "_live/tasks/" + id + ".json", false},
		{"other table", leakWhereLiveWrite, "_root/memories/" + id + ".json", false},
		{"no table folder", leakWhereLiveWrite, "tasks/" + id + ".txt", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := []leakcheck.Finding{{Rule: leakcheck.RuleID, Where: tc.where + " " + tc.path, Path: tc.path, RowID: id, Label: "store northwind"}}
			out, moved := withoutSelfMatches(tc.where, fs, rows)
			if moved != tc.moved || (len(out) == 0) != tc.moved {
				t.Fatalf("moved %v, %d findings left; want moved %v", moved, len(out), tc.moved)
			}
		})
	}
	// Another note's id in a moved row file still blocks (only the self
	// match is dropped).
	const other = "mem_0190aaaa0000700080000000000000b2"
	rows2 := []leakcheck.IndexRow{rows[0], {ID: other, Table: "memories", Label: "store northwind"}}
	p := "_root/tasks/" + id + ".json"
	out, moved := withoutSelfMatches(leakWhereLiveWrite, []leakcheck.Finding{
		{Rule: leakcheck.RuleID, Path: p, RowID: id}, {Rule: leakcheck.RuleRef, Path: p, RowID: other},
	}, rows2)
	if !moved || len(out) != 1 || out[0].RowID != other {
		t.Fatalf("moved %v, left %+v; want the other note's finding only", moved, out)
	}
	// Advice: a commit's non-row file is told to remove the text, never
	// that it is a task in this repo.
	if got := leakFix(LeakWhereCommit, "docs/tasks/"+id+".json"); strings.Contains(got, "is a task in this repo") || !strings.HasPrefix(got, "Remove the internal text from docs/tasks/") {
		t.Fatalf("commit hint for a non-row file: %q", got)
	}
	if got := leakFix(LeakWhereCommit, ".lore/data/tasks/"+id+".json"); !strings.Contains(got, id+" is a task in this repo") {
		t.Fatalf("commit hint for a row file: %q", got)
	}
}

// TestLeakRosterActorCopyIsNoMove: the roster is in the leak index
// (leakRosterRows), but an actor or team is never moved into a client
// repo, so the actor rule's finding on a client actor copy carrying more
// than the projection is not dropped as a self match (a stable_key in the
// copy committed before), and no live row is named a move for it.
func TestLeakRosterActorCopyIsNoMove(t *testing.T) {
	t.Parallel()
	const actor = "act_0190aaaa0000700080000000000000c3"
	rows := []leakcheck.IndexRow{{ID: actor, Table: "actors", Label: "store northwind/org"}}
	for _, p := range []string{".lore/data/actors/" + actor + ".json", "_root/actors/" + actor + ".json"} {
		fs := []leakcheck.Finding{{Rule: leakcheck.RuleActor, Path: p, RowID: actor}}
		out, moved := withoutSelfMatches(leakWhereLiveWrite, fs, rows)
		if moved || len(out) != 1 {
			t.Fatalf("%s: moved %v, %d findings left; want the actor finding kept, no move", p, moved, len(out))
		}
	}
}

// TestLiveRowDoc: the docs of a live write, a landing and a ride-along
// commit are row files at their live path; any other action's, or a
// reserved live folder's, are not.
func TestLiveRowDoc(t *testing.T) {
	t.Parallel()
	const id = "tsk_0190aaaa0000700080000000000000a1"
	cases := []struct {
		where, path string
		want        bool
	}{
		{leakWhereLiveWrite, "_root/tasks/" + id + ".json", true},
		{leakWhereLanding, "apps/web/tasks/" + id + ".json", true},
		{leakWhereRide, "_root/tasks/" + id + ".json", true},
		{LeakWhereCommit, "_root/tasks/" + id + ".json", false},
		{leakWhereLiveWrite, "_live/tasks/" + id + ".json", false},
		{leakWhereLiveWrite, "src/main.go", false},
	}
	for _, c := range cases {
		if got := liveRowDoc(c.where, c.path); got != c.want {
			t.Errorf("liveRowDoc(%q, %q) = %v, want %v", c.where, c.path, got, c.want)
		}
	}
}
