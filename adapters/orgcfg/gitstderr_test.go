package orgcfg

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitfault"
)

// TestGitStderrOf (G-16): the part of a git error that git wrote, so
// gitfault reads "does not appear to be a git repository" even after a
// long command line that pushed it past the reason's length bound.
func TestGitStderrOf(t *testing.T) {
	t.Parallel()
	long := "orgcfg: git -C /home/x/.lore/stores/acme/notes fetch --quiet --prune origin " + strings.Repeat("+refs/heads/*:refs/remotes/origin/* ", 8) +
		": exit status 128: fatal: '/srv/notes.git' does not appear to be a git repository\nfatal: Could not read from remote repository."
	got := gitStderrOf(errors.New(long))
	if !strings.HasPrefix(got, "fatal: '/srv/notes.git'") {
		t.Fatalf("gitStderrOf = %q", got)
	}
	if k := gitfault.Classify(gitfault.Reason(got)); k != gitfault.Missing {
		t.Fatalf("reason of %q classified %s, want missing", got, k)
	}
	if got := gitStderrOf(errors.New("plain")); got != "plain" {
		t.Fatalf("no git text: %q", got)
	}
}
