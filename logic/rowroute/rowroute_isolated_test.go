package rowroute

import "testing"

// TestDecideIsolatedBranch: on an isolated code branch a new row of a live
// kind stays with the code; --live, an already-live row and a no-live
// kind are as before (BRANCH_LAYERS 4, code-repo mode).
func TestDecideIsolatedBranch(t *testing.T) {
	t.Parallel()
	def := map[string]bool{"tasks": true, "rules": false}
	iso := Request{Defaults: def, Isolated: true}
	cases := []struct {
		name string
		req  Request
		row  Row
		want Place
	}{
		{"new task stays with the code", iso, Row{Table: "tasks"}, PlaceBranch},
		{"new task on a shared branch goes live", Request{Defaults: def}, Row{Table: "tasks"}, PlaceLive},
		{"--live still goes live", Request{Defaults: def, Isolated: true, Choice: ChoiceLive, ChoiceTable: "tasks"}, Row{Table: "tasks"}, PlaceLive},
		{"a live task stays live", iso, Row{Table: "tasks", InLive: true}, PlaceLive},
		{"a rule stays with the code", iso, Row{Table: "rules"}, PlaceBranch},
	}
	for _, c := range cases {
		if got := Decide(c.req, c.row).Place; got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// the companion rule follows: no content row goes live, so a new
	// supporting row stays with the code too
	ds := DecideAll(iso, []Row{{Table: "tasks"}, {Table: "actors"}})
	if ds[0].Place != PlaceBranch || ds[1].Place != PlaceBranch {
		t.Fatalf("DecideAll: %+v", ds)
	}
}
