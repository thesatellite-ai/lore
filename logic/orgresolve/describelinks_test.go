package orgresolve

import (
	"strings"
	"testing"
)

// TestDescribeLinksShowsAPathStore (devco:D-564): a link in a store kept
// at a path on this machine is keyed "_local/<path>"; whoami's "matched
// by" names the path, never the internal key.
func TestDescribeLinksShowsAPathStore(t *testing.T) {
	t.Parallel()
	got := describeLinks([]Link{
		{StoreURL: "_local/srv/elsewhere/lore-team", Folder: "app", Default: true},
		{StoreURL: "git.example.test/acme/lore", Folder: "web"},
	})
	if want := "/srv/elsewhere/lore-team app/ (default), git.example.test/acme/lore web/"; got != want {
		t.Fatalf("describeLinks = %q, want %q", got, want)
	}
	if strings.Contains(got, "_local/") {
		t.Fatalf("describeLinks shows the internal key: %q", got)
	}
}
