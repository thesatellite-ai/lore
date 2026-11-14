package sqlite

import (
	"fmt"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestRefreshPreparesOncePerStatement (migfix:n2): a refresh prepares each
// of its statements once per transaction, not once per row; preparing
// again for every row was half of a 5,000-row refresh. Not parallel: it
// reads the package's prepare counter.
func TestRefreshPreparesOncePerStatement(t *testing.T) {
	f := newFixture(t)
	prj := newPrj()
	refresh := func(name string, n int) int64 {
		t.Helper()
		c := f.open(name)
		rows := make([]store.Row, 0, n)
		for i := range n {
			rows = append(rows, f.memory(newMem(), prj, fmt.Sprintf("memory %d about statements", i)))
		}
		before := prepares.Load()
		f.apply(c, "repo", "c1", rows...)
		return prepares.Load() - before
	}
	small, big := refresh("small.db", 20), refresh("big.db", 400)
	if small == 0 {
		t.Fatal("no statement went through the per-transaction cache")
	}
	if big != small {
		t.Fatalf("prepares: %d for 20 rows, %d for 400 rows; want the same (once per statement)", small, big)
	}
}
