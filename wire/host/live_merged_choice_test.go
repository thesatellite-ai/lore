package host

// live_merged_choice_test.go — t-e2e6 (99e8ae9b), part 2: what the repo
// store's live backend does with the merged stamp's live choice (wire
// TestMergedStampAsksForTheLiveRef pins that the stamp asks for it). A done
// task sits in main's data (the branch), not on the live ref. With a live
// session the stamp asked for live goes on the live ref and the branch copy
// is left as it was, so a checkout that only listed its tasks keeps a clean
// .lore/data; without the choice the same edit stays a branch write (F1);
// with LORE_LIVE off there is no session and the stamp is a branch write
// (v0.1.17 recordMergedBranch).

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// mergedChoice is the choice wire's mergedStampLive puts on the ctx.
func mergedChoice(ctx context.Context) context.Context {
	return store.WithLiveChoice(ctx, store.LiveChoice{Mode: store.LiveOn, Table: schema.TableTasks})
}

// TestLiveBackend_MergedStampChoice covers the three placements.
func TestLiveBackend_MergedStampChoice(t *testing.T) {
	for _, c := range []struct {
		name     string
		choice   bool
		wantLive bool
	}{
		{"live session, stamp asks for live", true, true},
		{"live session, no choice (any other edit)", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newLiveFixture(t)
			done := ltRow(t, schema.TableTasks, ltTaskA, 1, "done, not merged", false)
			if err := f.inner.Write(f.ctx, []store.Change{{Row: done}}); err != nil {
				t.Fatal(err)
			}
			ctx := f.ctx
			if c.choice {
				ctx = mergedChoice(ctx)
			}
			stamp := ltRow(t, schema.TableTasks, ltTaskA, 2, "merged", false)
			if err := f.b.Write(ctx, []store.Change{{Row: stamp, Expect: done.HLC}}); err != nil {
				t.Fatal(err)
			}
			if got := f.liveHas(t, schema.TableTasks, ltTaskA); got != c.wantLive {
				t.Fatalf("on the live ref = %v, want %v", got, c.wantLive)
			}
			branch, err := f.inner.Get(f.ctx, done.Table, done.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantBranch := stamp
			if c.wantLive {
				wantBranch = done // main's data untouched: nothing for the checkout to commit
			}
			if branch.HLC.Compare(wantBranch.HLC) != 0 {
				t.Fatalf("branch copy at %v, want %v", branch.HLC, wantBranch.HLC)
			}
			got, err := f.b.Get(f.ctx, done.Table, done.ID)
			if err != nil || got.HLC.Compare(stamp.HLC) != 0 {
				t.Fatalf("a reader sees the stamp: %v at %v", err, got.HLC)
			}
		})
	}

	t.Run("LORE_LIVE off, stamp asks for live", func(t *testing.T) {
		f := newLiveFixture(t)
		f.s.release()
		t.Setenv(EnvLive, envOff)
		inner := dirMem{Backend: memstore.NewData().Open(store.OpenOptions{}), dir: t.TempDir()}
		lb, ok := (&liveHost{}).wrap(store.SourceRepo, inner).(*liveBackend)
		if !ok || lb.session(f.ctx) != nil {
			t.Fatal("LORE_LIVE=0 must leave the repo store without a live session")
		}
		done := ltRow(t, schema.TableTasks, ltTaskA, 1, "done, not merged", false)
		if err := inner.Write(f.ctx, []store.Change{{Row: done}}); err != nil {
			t.Fatal(err)
		}
		stamp := ltRow(t, schema.TableTasks, ltTaskA, 2, "merged", false)
		if err := lb.Write(mergedChoice(f.ctx), []store.Change{{Row: stamp, Expect: done.HLC}}); err != nil {
			t.Fatal(err)
		}
		branch, err := inner.Get(f.ctx, done.Table, done.ID)
		if err != nil || branch.HLC.Compare(stamp.HLC) != 0 {
			t.Fatalf("with LORE_LIVE off the stamp is a branch write: %v at %v", err, branch.HLC)
		}
		if f.liveHas(t, schema.TableTasks, ltTaskA) {
			t.Fatal("with LORE_LIVE off nothing reaches the live ref")
		}
	})
}
