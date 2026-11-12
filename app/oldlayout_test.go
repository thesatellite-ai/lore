package app

import "testing"

// TestRunsOldLayoutMove: the first-run move of an earlier build's stores
// runs for a person's commands, never inside git (merge driver, the repo
// hooks) or the agent's session hooks, nor for help and version.
func TestRunsOldLayoutMove(t *testing.T) {
	for _, args := range [][]string{{"doctor"}, {"task", "list"}, {"--trace", "sync"}, {"store", "list"}, {"memory", "add", "--body", "x", "--", "--help"}} {
		if !runsOldLayoutMove(args) {
			t.Errorf("%v does not run the move", args)
		}
	}
	for _, args := range [][]string{{}, {"version"}, {"--version"}, {"help"}, {"migrate", "run", "--help"}, {"fix", "-h"}, {"task", "list", "--help=true"}, {"merge-driver", "a", "b", "c"}, {"sync", "hook", "post-commit"}, {"agent", "end"}, {"agent", "start", "--hook-stdin"}} {
		if runsOldLayoutMove(args) {
			t.Errorf("%v runs the move", args)
		}
	}
}
