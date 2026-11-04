package tasktree

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

func chain(n int) []Node {
	var out []Node
	prev := ""
	for i := range n {
		id := string(rune('a' + i))
		out = append(out, Node{ID: id, Parent: prev})
		prev = id
	}
	return out
}

func TestTreeShape(t *testing.T) {
	tr := Build([]Node{
		{ID: "p"}, {ID: "c2", Parent: "p", Rank: "a2"}, {ID: "c1", Parent: "p", Rank: "a1", State: Done},
		{ID: "c3", Parent: "p", Rank: "a2", State: Cancelled}, {ID: "g", Parent: "c2"}, {ID: "orphan", Parent: "gone"},
	})
	if got := tr.Children("p"); !slices.Equal(got, []string{"c1", "c2", "c3"}) {
		t.Fatalf("children order %v", got)
	}
	if got := tr.Roots(); !slices.Equal(got, []string{"orphan", "p"}) {
		t.Fatalf("roots %v", got)
	}
	if tr.Depth("g") != 3 || tr.Height("p") != 3 || tr.Height("g") != 1 {
		t.Fatalf("depth %d height %d", tr.Depth("g"), tr.Height("p"))
	}
	if tr.IsLeaf("p") || !tr.IsLeaf("c1") || !tr.IsLeaf("g") {
		t.Fatal("leaf")
	}
	want := Rollup{ChildrenTotal: 2, ChildrenDone: 1, LeavesTotal: 2, LeavesDone: 1}
	if got := tr.Rollup("p"); got != want {
		t.Fatalf("rollup %+v", got)
	}
	if got := tr.Descendants("p"); !slices.Equal(got, []string{"c1", "c2", "g", "c3"}) {
		t.Fatalf("descendants %v", got)
	}
}

func TestCycleSmallestIDIsTop(t *testing.T) {
	// a merge left x → y → z → x; every machine must read x as top level.
	for _, order := range [][]Node{
		{{ID: "y", Parent: "z"}, {ID: "x", Parent: "y"}, {ID: "z", Parent: "x"}},
		{{ID: "z", Parent: "x"}, {ID: "x", Parent: "y"}, {ID: "y", Parent: "z"}},
	} {
		tr := Build(order)
		if !slices.Equal(tr.Roots(), []string{"x"}) || len(tr.Cycles()) != 1 || tr.Cycles()[0][0] != "x" {
			t.Fatalf("roots %v cycles %v", tr.Roots(), tr.Cycles())
		}
		if tr.Depth("y") != 3 {
			t.Fatalf("depth y %d", tr.Depth("y"))
		}
	}
}

func TestCheckMove(t *testing.T) {
	tr := Build(chain(4)) // a → b → c → d
	if err := tr.CheckMove("a", "d", 8); !errs.Has(err, errs.Cycle) {
		t.Fatalf("cycle: %v", err)
	}
	if err := tr.CheckMove("a", "a", 8); !errs.Has(err, errs.Cycle) {
		t.Fatalf("self: %v", err)
	}
	if err := tr.CheckMove("new", "d", 4); !errs.Has(err, errs.TreeDepth) {
		t.Fatalf("depth: %v", err)
	}
	if err := tr.CheckMove("new", "c", 4); err != nil {
		t.Fatalf("depth 4 ok: %v", err)
	}
	// moving b (height 3) under a fresh top task z: depth 4 fits 4, not 3
	tr = Build(append(chain(4), Node{ID: "z"}))
	if err := tr.CheckMove("b", "z", 4); err != nil {
		t.Fatal(err)
	}
	if err := tr.CheckMove("b", "z", 3); !errs.Has(err, errs.TreeDepth) {
		t.Fatalf("subtree depth: %v", err)
	}
	if err := tr.CheckMove("b", "", 1); err != nil {
		t.Fatal(err)
	}
}

func TestRanks(t *testing.T) {
	r := ""
	var seq []string
	for range 200 {
		r = After(r)
		seq = append(seq, r)
	}
	if !slices.IsSorted(seq) || len(slices.Compact(slices.Clone(seq))) != 200 {
		t.Fatal("After must increase")
	}
	cases := [][2]string{{"", "a1"}, {"a1", "a2"}, {"a1", "a1V"}, {"", "1"}, {"Z", "a1"}, {"a", "a1"}, {"a1", ""}}
	for _, c := range cases {
		m := Between(c[0], c[1])
		if m <= c[0] || (c[1] != "" && m >= c[1]) || !valid(m) {
			t.Fatalf("Between(%q,%q) = %q", c[0], c[1], m)
		}
	}
	// repeated inserts at the front stay ordered
	lo, hi := "", "a1"
	for range 100 {
		m := Between(lo, hi)
		if m >= hi || m <= lo {
			t.Fatalf("front %q %q %q", lo, m, hi)
		}
		hi = m
	}
	if got := Between("b", "a"); got != After("b") {
		t.Fatalf("unordered fallback %q", got)
	}
}
