package host

// live_actor_upkeep_test.go — devco:K-6: lore's own refresh of the
// writer's actor copy (Change.Upkeep) in a run that writes a live task
// rides live with the task, so the committed actor file in the branch data
// is left alone; a person's explicit edit of the same row is written to the
// branch data at once, as before.

import (
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

func TestLiveBackend_ActorUpkeepRidesLive(t *testing.T) {
	for _, c := range []struct {
		name       string
		upkeep     bool
		wantBranch bool // the branch data holds the edited copy right after the write
	}{
		{name: "upkeep rides with the live task", upkeep: true, wantBranch: false},
		{name: "an explicit edit goes to the branch at once", upkeep: false, wantBranch: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newLiveFixture(t)
			v1 := ltRow(t, schema.TableActors, ltActorID, 1, "Ada", false)
			if err := f.inner.Write(f.ctx, []store.Change{{Row: v1}}); err != nil {
				t.Fatal(err)
			}
			v2 := ltRow(t, schema.TableActors, ltActorID, 2, "Ada Lovelace", false)
			if err := f.b.Write(f.ctx, []store.Change{{Row: v2, Expect: v1.HLC, Upkeep: c.upkeep}}); err != nil {
				t.Fatal(err)
			}
			got, err := f.inner.Get(f.ctx, schema.TableActors, v1.ID)
			if err != nil {
				t.Fatal(err)
			}
			if inBranch := sameFile(got, v2); inBranch != c.wantBranch {
				t.Fatalf("branch data holds the edited copy: %v, want %v", inBranch, c.wantBranch)
			}
			if err = f.b.Write(f.ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 3, "open work", false)}}); err != nil {
				t.Fatal(err)
			}
			if !f.liveHas(t, schema.TableTasks, ltTaskA) {
				t.Fatal("a new task goes live")
			}
			if !c.upkeep {
				return
			}
			if !f.liveHas(t, schema.TableActors, ltActorID) {
				t.Fatal("the actor upkeep rides live with the task")
			}
			if err = f.s.flushCompanions(f.ctx); err != nil {
				t.Fatal(err)
			}
			if got, err = f.inner.Get(f.ctx, schema.TableActors, v1.ID); err != nil || !sameFile(got, v1) {
				t.Fatalf("the committed actor file is left as it was after the run: %v", err)
			}
			if r, err := f.b.Get(f.ctx, schema.TableActors, v1.ID); err != nil || !sameFile(r, v2) {
				t.Fatalf("a reader sees the refreshed copy: %v", err)
			}
		})
	}
}
