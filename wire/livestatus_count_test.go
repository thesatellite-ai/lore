package wire

import (
	"context"
	"fmt"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// countingGets is a backend that counts Get calls.
type countingGets struct {
	store.Backend
	gets int
}

// Get implements store.Backend.
func (c *countingGets) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	c.gets++
	return c.Backend.Get(ctx, table, id)
}

// TestMigrateStatusLooksUpOldLiveRowsOnly (D-217): the migration status of
// every lore doctor and lore fix counts the live rows older than the row
// format; it once read each live row of the repo back from its store, one
// Get per open live task (2,400 on a big repo), to keep the few old ones.
// Now only the old rows are looked up, and they are still counted.
func TestMigrateStatusLooksUpOldLiveRowsOnly(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	project := projectID(t, a)
	files := map[string]string{livePath(liveOnlyMem): liveMemory(liveOnlyMem, project, "old live row", liveOld)}
	const current = 30
	for i := range current {
		id := fmt.Sprintf("mem_01a1a59b891c70b789144cd3e7%06x", 0x100+i)
		files[livePath(id)] = `{"_table":"memories","_v":2,"body":"current","created_at":"2026-11-04T08:09:56Z","id":"` + id +
			`","kind":"retrieved","project_id":"` + project + `","source_kind":"manual","updated_at":"2026-11-04T08:09:56Z","_live":{"hlc":"` + liveOld + `"}}`
	}
	writeLiveRef(t, repo, liveRefLocal, files)
	st, err := a.state(background())
	if err != nil {
		t.Fatal(err)
	}
	c := &countingGets{Backend: st.byName[store.SourceRepo]}
	st.byName[store.SourceRepo] = c
	defer func() { st.byName[store.SourceRepo] = c.Backend }()
	p, err := pendingOldLiveRows(background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 1 || p[0].live.row.ID.String() != liveOnlyMem {
		t.Fatalf("old live rows: %d, want only %s", len(p), liveOnlyMem)
	}
	if c.gets != 1 {
		t.Errorf("the status read %d live rows back from the store, want 1 (the old one; %d current rows skipped)", c.gets, current)
	}
}
