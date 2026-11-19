package branch

import (
	"strings"
	"testing"
)

func TestRowsText(t *testing.T) {
	t.Parallel()
	got := RowsText(Counts(map[string]int{"tasks": 2, "memories": 1, "task_lists": 1, "policies": 3}))
	want := "1 memory, 3 policies, 1 task list, 2 tasks"
	if got != want {
		t.Fatalf("RowsText = %q, want %q", got, want)
	}
	if RowsText(nil) != "no rows" {
		t.Fatal("empty")
	}
}

func TestLandOutText(t *testing.T) {
	t.Parallel()
	rows := Counts(map[string]int{"rules": 1})
	cases := []struct {
		out  LandOut
		want string
	}{
		{LandOut{Branch: "feat/x", Folder: "app", Nothing: true}, "no notes of feat/x in app: nothing to land"},
		{LandOut{Branch: "feat/x", Folder: "app", AlreadyLanded: "1a2b3c4d5e"}, "already landed: the notes of feat/x are in app as commit 1a2b3c4"},
		{LandOut{Branch: "feat/x", Folder: "app", DryRun: true, Rows: rows, StoreBranch: "main"}, "would land 1 rule of feat/x into app on main (nothing changed)"},
		{LandOut{Branch: "feat/x", Folder: "app", Rows: rows, StoreBranch: "main", Commit: "9f8e7d6c5b", Merged: 2, Pushed: true}, "✓ landed 1 rule of feat/x into app: commit 9f8e7d6 on main (pushed to the store's remote)"},
		{LandOut{Branch: "feat/b", Folder: "app", Rows: rows, StoreBranch: "main", Into: "feat/a", Pushed: true}, "✓ landed 1 rule of feat/b into the notes of feat/a (pushed to the store's remote)"},
	}
	for _, c := range cases {
		if got := c.out.Text(); !strings.HasPrefix(got, c.want) {
			t.Errorf("Text() = %q, want prefix %q", got, c.want)
		}
	}
}

// TestOpsHaveNamedFlagsOnly: every example passes values only after a
// named flag (standing rule: named flags only, no positional values).
func TestOpsHaveNamedFlagsOnly(t *testing.T) {
	t.Parallel()
	for _, e := range Ops(nil) {
		for _, ex := range e.Spec().Examples {
			words := strings.Fields(ex.CLI)
			if len(words) < 3 || words[0] != "lore" || words[1] != "branch" {
				t.Errorf("%s example %q is not a lore branch command", e.Spec().Name, ex.CLI)
				continue
			}
			prevFlag := false
			for _, w := range words[3:] {
				isFlag := strings.HasPrefix(w, "--")
				if !isFlag && !prevFlag {
					t.Errorf("%s example %q has a positional value %q", e.Spec().Name, ex.CLI, w)
				}
				prevFlag = isFlag
			}
		}
	}
}

// TestPushingVerbsSayTheyPushD614 (devco D-614): land, drop, restore,
// purge and rename push to the store's remote at once with no --push (they
// are explicit remote acts), so each one's help line says it pushes, and
// purge's output says where the purge went; the verbs that write locally
// (promote, isolate, share, adopt) never claim a push.
func TestPushingVerbsSayTheyPushD614(t *testing.T) {
	t.Parallel()
	pushing := map[string]bool{OpLand: true, OpDrop: true, OpRestore: true, OpPurge: true, OpRename: true}
	seen := 0
	for _, e := range Ops(nil) {
		s := e.Spec()
		says := strings.Contains(s.Summary, "push")
		switch {
		case pushing[s.Name] && !says:
			t.Errorf("%s pushes at once but its help does not say so: %q", s.Name, s.Summary)
		case !pushing[s.Name] && says:
			t.Errorf("%s writes locally but its help speaks of a push: %q", s.Name, s.Summary)
		}
		if pushing[s.Name] {
			seen++
		}
	}
	if seen != len(pushing) {
		t.Fatalf("found %d of the %d pushing verbs in Ops", seen, len(pushing))
	}
	if got := (PurgeOut{Purged: []string{"refs/lore/trash/x"}}).Text(); !strings.Contains(got, "pushed to the store's remote") {
		t.Errorf("purge output does not say it pushed: %q", got)
	}
}
