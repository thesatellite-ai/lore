package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"saas/pkg/aicoder/leakcheck"
)

// Coordinator Q13: a `Lore-Task: <store task id>` trailer in a client
// commit message is a pointer to an internal row and IS blocked — the
// trailer line is never exempt; only git's "#" comments are dropped.
func TestCommitMessageTrailerIsChecked(t *testing.T) {
	const storeTask = "tsk_0b770000000000000000000000000001"
	idx := leakcheck.NewIndex([]leakcheck.IndexRow{{ID: storeTask, Table: "tasks", Label: "[northwind/acme]", Texts: []string{"invite mutation"}}}, nil)
	cases := []struct {
		name  string
		msg   string
		block bool
	}{
		{"trailer naming a store task", "fix checkout\n\nLore-Task: " + storeTask + "\n", true},
		{"store id in a git comment line", "fix checkout\n# Lore-Task: " + storeTask + "\n", false},
		{"no internal id", "fix checkout\n\nLore-Task: tsk_ffff0000000000000000000000000002\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			msg := filepath.Join(dir, "COMMIT_EDITMSG")
			if err := os.WriteFile(msg, []byte(c.msg), 0o600); err != nil {
				t.Fatal(err)
			}
			it, err := commitMessageItem(context.Background(), dir, msg)
			if err != nil {
				t.Fatal(err)
			}
			fs := leakcheck.Blocking(leakcheck.Check([]leakcheck.Item{it}, idx, leakcheck.Options{ClientRepo: true}))
			if (len(fs) > 0) != c.block {
				t.Fatalf("blocked=%v want %v: %+v", len(fs) > 0, c.block, fs)
			}
			if c.block && (fs[0].RowID != storeTask || fs[0].Where != whereCommitMessage) {
				t.Fatalf("finding must name the store task in the commit message: %+v", fs[0])
			}
		})
	}
}
