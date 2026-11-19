package orgresolve

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestOwnRepoOverlaps: devco D-585, an own pattern covering repos entries
// is named with the count, the first entries and the rule that wins.
func TestOwnRepoOverlaps(t *testing.T) {
	repos := []string{"github.com/acme/a", "github.com/acme/b", "github.com/acme/c", "github.com/acme/d", "github.com/other/x"}
	got := OwnRepoOverlaps([]string{"github.com/acme/*", "github.com/solo/*"}, repos)
	if len(got) != 1 {
		t.Fatalf("want one overlap line, got %q", got)
	}
	for _, want := range []string{"own github.com/acme/* overlaps 4 repos entries", "github.com/acme/a, github.com/acme/b, github.com/acme/c and 1 more", "repos entries win (client)"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("line %q lacks %q", got[0], want)
		}
	}
	if one := OwnRepoOverlaps([]string{"github.com/other/*"}, repos); len(one) != 1 || !strings.Contains(one[0], "overlaps 1 repos entry (github.com/other/x)") {
		t.Fatalf("single overlap: %q", one)
	}
	if none := OwnRepoOverlaps([]string{"github.com/solo/*"}, repos); len(none) != 0 {
		t.Fatalf("no overlap wanted, got %q", none)
	}
	// the line states what ClassifyRemote does
	if r := ClassifyRemote(&orgconf.Org{Own: []string{"github.com/acme/*"}, Repos: repos}, []string{"github.com/acme/a"}); r.Role != RoleClient {
		t.Fatalf("repos must win over own, got %s", r.Role)
	}
}
