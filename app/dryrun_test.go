package app

import "testing"

// TestIsDryRun (B4-14): a --dry-run command must not run the first-run
// layout move, which writes clones and records.
func TestIsDryRun(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"fix", "--dry-run"}, true},
		{[]string{"migrate", "run", "--dry-run=true"}, true},
		{[]string{"fix", "--dry-run=false"}, false},
		{[]string{"memory", "add", "--body", "x"}, false},
		{[]string{"memory", "add", "--", "--dry-run"}, false},
	} {
		if got := isDryRun(c.args); got != c.want {
			t.Errorf("isDryRun(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}
