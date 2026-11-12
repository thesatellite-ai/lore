package app

import (
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// TestOfflineFixDryRun: `lore fix --dry-run` (and --all --dry-run) turns
// LORE_OFFLINE on for the process before anything opens a store; a plain
// lore fix and other commands leave it alone (owner bug 14).
func TestOfflineFixDryRun(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"fix", "--dry-run"}, true},
		{[]string{"fix", "--all", "--dry-run", "--jobs", "4"}, true},
		{[]string{"fix", "--dry-run=true"}, true},
		{[]string{"fix", "--all"}, false},
		{[]string{"doctor", "--dry-run"}, false},
		{[]string{"memory", "add", "--dry-run"}, false},
	}
	for _, c := range cases {
		t.Setenv(gitenv.EnvOffline, "")
		offlineFixDryRun(c.args)
		if got := os.Getenv(gitenv.EnvOffline) == gitenv.On; got != c.want {
			t.Errorf("%v: offline %v, want %v", c.args, got, c.want)
		}
	}
}
