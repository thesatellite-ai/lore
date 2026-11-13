package wire

// Ports of saas/cmd/cli leak_prepush_test.go TestLeakFixNamesTheActionableRow
// and check_cmd_test.go TestCommitMessageTrailerIsChecked (B11) to cmd/lore's
// F16 check (leakdocs.go leakFix, check_standalone.go stripGitComments).

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
)

// TestLeakFixNamesTheActionableRow (F10, F11): the fix a refusal offers
// names what the user can act on: the repo row of a commit or a push (never
// the internal note it matched), the file otherwise; a move refusal never
// tells the user to move again.
func TestLeakFixNamesTheActionableRow(t *testing.T) {
	t.Parallel()
	const (
		mem = "mem_0b770000000000000000000000000001"
		rul = "rul_0d000000000000000000000000000003"
	)
	rowFile := ".lore/data/rules/" + rul + ".json"
	cases := []struct {
		name, where, path string
		must, mustNot     []string
	}{
		{"commit of a leaking repo rule", LeakWhereCommit, rowFile, []string{"lore move --id " + rul + " --to <org>", "lore rule delete --id " + rul + " --confirm"}, []string{mem}},
		{"commit of a monorepo folder's row", LeakWhereCommit, "svc/api/" + rowFile, []string{"lore move --id " + rul + " --to <org>"}, nil},
		{"push of a leaking repo rule", LeakWherePush, rowFile, []string{rul, "rewrite that commit"}, []string{"lore move --id " + mem}},
		{"push of a source file", LeakWherePush, "src/a.ts", []string{"Remove the internal text from src/a.ts", "rewrite the commit"}, []string{"lore move"}},
		{"commit of a source file", LeakWhereCommit, "src/a.ts", []string{"Remove the internal text from src/a.ts"}, []string{"lore move"}},
		{"commit of a memory row", LeakWhereCommit, ".lore/data/memories/" + mem + ".json", []string{"lore memory delete --id " + mem}, nil},
		{"move quoting another note", leakWhereMove, ".lore/data/memories/" + mem + ".json", []string{"Not moved", "Remove that part from " + mem}, []string{"lore move --id " + mem + " --to"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := leakFix(c.where, c.path)
			for _, s := range c.must {
				if !strings.Contains(got, s) {
					t.Fatalf("fix %q must say %q", got, s)
				}
			}
			for _, s := range c.mustNot {
				if strings.Contains(got, s) {
					t.Fatalf("fix %q must not say %q", got, s)
				}
			}
		})
	}
}

// TestCommitMessageTrailerIsChecked (coordinator Q13): a `Lore-Task: <store
// task id>` trailer in a client commit message points at an internal row
// and IS blocked; only git's "#" comment lines are dropped.
func TestCommitMessageTrailerIsChecked(t *testing.T) {
	t.Parallel()
	const storeTask = "tsk_0b770000000000000000000000000001"
	idx := leakcheck.NewIndex([]leakcheck.IndexRow{{ID: storeTask, Table: "tasks", Label: "[northwind/acme]", Texts: []string{"invite mutation"}}}, nil)
	cases := []struct {
		name, msg string
		block     bool
	}{
		{"trailer naming a store task", "fix checkout\n\nLore-Task: " + storeTask + "\n", true},
		{"store id in a git comment line", "fix checkout\n# Lore-Task: " + storeTask + "\n", false},
		{"no internal id", "fix checkout\n\nLore-Task: tsk_ffff0000000000000000000000000002\n", false},
		{"indented id is not a comment", "fix checkout\n  # see " + storeTask + "\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it := leakcheck.Item{Where: checkWhereCommitMsg, Content: stripGitComments([]byte(c.msg))}
			fs := leakcheck.Blocking(leakcheck.Check([]leakcheck.Item{it}, idx, leakcheck.Options{ClientRepo: true}))
			if (len(fs) > 0) != c.block {
				t.Fatalf("blocked=%v want %v: %+v", len(fs) > 0, c.block, fs)
			}
			if c.block && fs[0].RowID != storeTask {
				t.Fatalf("finding must name the store task: %+v", fs[0])
			}
		})
	}
}
