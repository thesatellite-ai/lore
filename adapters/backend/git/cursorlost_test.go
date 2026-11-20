package git

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestCursorLostAfterHandReset: a cursor on the clone's HEAD, or behind a
// HEAD that descends from it (a later write, a hand commit removing a file),
// is not lost; after `git reset --hard` to an older commit, and for a
// commit the clone does not hold, it is (owner P-5, devco D-763: the cache
// kept the rows the reset dropped).
func TestCursorLostAfterHandReset(t *testing.T) {
	t.Parallel()
	b, g, ctx := clone(t, newRemote(t, true)), newGen(t), context.Background()
	write(t, b, store.Change{Row: g.row(newID(t), "kept", hlc.HLC{})})
	old := strings.TrimSpace(gitT(t, b.Dir(), "rev-parse", "HEAD"))
	write(t, b, store.Change{Row: g.row(newID(t), "dropped later", hlc.HLC{})})
	head := strings.TrimSpace(gitT(t, b.Dir(), "rev-parse", "HEAD"))
	lost := func(c string) bool {
		t.Helper()
		got, err := b.CursorLost(ctx, store.Cursor(c))
		if err != nil {
			t.Fatalf("CursorLost(%s): %v", c, err)
		}
		return got
	}
	for _, c := range []string{"", head, old, "not-a-commit"} {
		if lost(c) {
			t.Fatalf("cursor %q lost before any reset (HEAD %s)", c, head)
		}
	}
	gitT(t, b.Dir(), "reset", "--quiet", "--hard", old)
	if !lost(head) {
		t.Fatalf("cursor %s still usable after the clone was reset to %s", head, old)
	}
	if lost(old) {
		t.Fatal("the cursor on HEAD itself is lost")
	}
	if !lost(strings.Repeat("ab", 20)) {
		t.Fatal("a commit the clone does not hold is not lost")
	}
}
