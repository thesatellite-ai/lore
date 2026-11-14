package host

import (
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLiveBackend_FetchMovesTheReadCursor is G-3: a row a teammate pushed
// to origin/<default>, fetched but not pulled, is read through by a full
// Read (lists showed it), and the cache's next Read with its cursor must
// carry it too, or `lore search` never finds a row `memory list` shows.
// The cursor named only the live ref's commit, which a fetch does not move.
func TestLiveBackend_FetchMovesTheReadCursor(t *testing.T) {
	f := newLiveFixture(t)
	dir := f.b.top
	_, cur, err := f.b.Read(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// A teammate's memory lands on origin/main; this checkout only fetched.
	landed := ltRow(t, schema.TableMemories, ltMemory, 2, "naïve approach", false)
	rtCommitFile(t, dir, landed)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "teammate")
	gitT(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitT(t, dir, "reset", "-q", "--hard", "HEAD~1")
	if f.s.cache != nil {
		f.s.cache.Invalidate() // a new command's run reads git afresh
	}

	_, cur2, err := f.b.Read(f.ctx, cur)
	if err != nil {
		t.Fatal(err)
	}
	if cur2 == cur {
		t.Fatalf("the cursor must change when origin/main moves: %q", cur2)
	}
	// Nothing moved since: the next Read is incremental again.
	rows, _, err := f.b.Read(f.ctx, cur2)
	if err != nil {
		t.Fatal(err)
	}
	if holdsKey(rows, landed.Key()) {
		t.Fatalf("with nothing moved the Read is incremental, not a full re-read")
	}
}

// holdsKey reports whether rows holds a row with key k.
func holdsKey(rows []store.Row, k store.Key) bool {
	for _, r := range rows {
		if r.Key() == k {
			return true
		}
	}
	return false
}
