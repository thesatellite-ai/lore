package host

// live_companion_flush_test.go — later 77 (r2-regress, FLAG6): the end of
// a live run writes the held actor rows no content row carried
// (flushCompanions). A held row whose identical file is already in the
// branch data (another write of the same run, or a teammate's identical
// row) is skipped, so the run never fails with E_STALE over a row that
// is already there; a different file at that id still reaches Write and
// the clash is reported.

import (
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestFlushCompanions_ExistingActorFile: matrix over what the branch data
// holds at the held actor's id when the run ends.
func TestFlushCompanions_ExistingActorFile(t *testing.T) {
	cases := []struct {
		name      string
		existing  func(t *testing.T) (store.Row, bool) // row placed in the branch data after the hold (false: none)
		wantStale bool
	}{
		{name: "absent: written", existing: func(*testing.T) (store.Row, bool) { return store.Row{}, false }},
		{name: "identical file: skipped", existing: func(t *testing.T) (store.Row, bool) {
			return ltRow(t, schema.TableActors, ltActorID, 1, "Ada", false), true
		}},
		{name: "different title: clash reported", wantStale: true, existing: func(t *testing.T) (store.Row, bool) {
			return ltRow(t, schema.TableActors, ltActorID, 1, "Grace", false), true
		}},
		{name: "different stamp: clash reported", wantStale: true, existing: func(t *testing.T) (store.Row, bool) {
			return ltRow(t, schema.TableActors, ltActorID, 2, "Ada", false), true
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newLiveFixture(t)
			held := ltRow(t, schema.TableActors, ltActorID, 1, "Ada", false)
			if err := f.b.Write(f.ctx, []store.Change{{Row: held}}); err != nil {
				t.Fatal(err)
			}
			if len(f.s.companions) != 1 {
				t.Fatalf("a new actor row is held as a companion: %d held", len(f.s.companions))
			}
			ex, has := c.existing(t)
			if has {
				if err := f.inner.Write(f.ctx, []store.Change{{Row: ex}}); err != nil {
					t.Fatal(err)
				}
			}
			err := f.s.flushCompanions(f.ctx)
			if c.wantStale {
				if !errors.Is(err, store.ErrStale) {
					t.Fatalf("a different file at the held id must reach Write and report the clash, got %v", err)
				}
				got, gerr := f.inner.Get(f.ctx, schema.TableActors, held.ID)
				if gerr != nil || !sameFile(got, ex) {
					t.Fatalf("the clash leaves the existing file as it was: %v", gerr)
				}
				return
			}
			if err != nil {
				t.Fatalf("flush: %v", err)
			}
			got, err := f.inner.Get(f.ctx, schema.TableActors, held.ID)
			if err != nil || !sameFile(got, held) {
				t.Fatalf("the actor row is in the branch data after the flush: %v", err)
			}
			if len(f.s.companions) != 0 {
				t.Fatal("a flushed companion is no longer held")
			}
			if f.liveHas(t, schema.TableActors, ltActorID) {
				t.Fatal("a lone actor row never goes live")
			}
		})
	}
}
