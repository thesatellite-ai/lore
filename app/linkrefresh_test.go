package app

import (
	"strings"
	"testing"
)

// TestOnlySyncRefreshesLinkIndex: `lore sync` fetches every org store
// whatever live.fetch_ttl says; the git hooks' `lore sync hook <name>` is
// not `lore sync`. Counted as sync, every hook of one plain commit fetched
// the org stores and asked their HEAD (ls-remote), a commit being local
// (devco:D-420).
func TestOnlySyncRefreshesLinkIndex(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args string
		want bool
	}{
		{"sync", true},
		{"sync --dry-run", true},
		{"--json sync", true},
		{"sync hook pre-commit", false},
		{"sync hook post-commit", false},
		{"sync hook pre-push origin /srv/git/app.git", false},
		{"sync hook refresh post-checkout", false},
		{"task list", false},
		{"task add hook", false},
	} {
		if got := refreshesLinkIndex(strings.Fields(c.args)); got != c.want {
			t.Errorf("refreshesLinkIndex(lore %s) = %v, want %v", c.args, got, c.want)
		}
	}
}
