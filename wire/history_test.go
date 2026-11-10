package wire

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// OV62: versions from the branch and the live ref are ordered by the row's
// HLC, not commit time; one edit seen on both is kept once; At/By come from
// the row's audit fields.
func TestOrderVersions(t *testing.T) {
	t.Parallel()
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	v := func(commit string, edit, committed time.Time) opstask.RowVersion {
		h := hlc.HLC{Wall: edit, Actor: actor}
		return opstask.RowVersion{Commit: commit, At: committed, By: "git-author", Row: row.Row{HLC: h, UpdatedAt: edit, UpdatedBy: actor}}
	}
	got := orderVersions([]opstask.RowVersion{
		v("branch-late", t0.Add(time.Hour), t0.Add(48*time.Hour)), // landed copy committed two days later
		v("live-1", t0, t0),
		v("live-2", t0.Add(time.Hour), t0.Add(time.Hour)), // same edit as branch-late
	})
	if len(got) != 2 || got[0].Commit != "live-1" || !got[1].At.Equal(t0.Add(time.Hour)) || got[1].By != actor.String() {
		t.Fatalf("ordered %+v", got)
	}
}
