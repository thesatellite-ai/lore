package host

import (
	"strings"
	"testing"
)

// TestMovedRemoteHint (devco:O-13): the "the repo moved" step names lore
// repo set-url only for a hosted remote (which records an address); for
// a path on this machine git remote set-url is the whole move, since
// lore repo set-url records no path.
func TestMovedRemoteHint(t *testing.T) {
	t.Parallel()
	if got := movedRemoteHint("origin", "/srv/git/kit.git"); strings.Contains(got, "lore repo set-url") || !strings.Contains(got, "git remote set-url origin <new-path>, then lore sync") {
		t.Fatalf("path remote: %q", got)
	}
	if got := movedRemoteHint("origin", "git@git.example.test:acme/kit.git"); !strings.Contains(got, "then lore repo set-url --url <new-url>") {
		t.Fatalf("hosted remote: %q", got)
	}
}
