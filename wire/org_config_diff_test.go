package wire

import (
	"strings"
	"testing"
)

// TestListDiffLinesD649: `org config set` prints the change of a pattern
// list as +/- lines and the size, never the whole list as was and now
// (devco D-649: 66 entries printed twice in the confirmation and again in
// the result); a change that changes nothing says so.
func TestListDiffLinesD649(t *testing.T) {
	t.Parallel()
	cur := []string{"github.com/acme/a", "github.com/acme/b", "github.com/acme/c"}
	got := strings.Join(listDiffLines(cur, []string{"github.com/acme/a", "github.com/acme/c", "github.com/partner/*"}), "\n")
	want := "  - github.com/acme/b\n  + github.com/partner/*\n  3 entries -> 3"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "github.com/acme/a") {
		t.Fatalf("an unchanged entry printed:\n%s", got)
	}
	if got := strings.Join(listDiffLines(cur, cur), "\n"); got != "  no change (3 entries)" {
		t.Fatalf("no change: %q", got)
	}
}
