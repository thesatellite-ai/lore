package host

// live_landed_stamp_test.go — later.md 239 (r2-regress, v0.1.17
// live-landed-stamp.diff): F9's lazy merged check stamps merged_at on a
// done task that already landed on origin/<default> and left live. Get
// reads that row through from origin's copy (live_landed.go), so the
// stamp's Expect names the landed copy; Write must check it against the
// same copy, not against "no row", or every lazy merged check fails
// E_STALE. Owner: g-coord (claimed in p3-owners.md).

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLiveBackend_StampOfALandedTask: on the default branch, a task on
// the fetched origin/main that is neither in the work tree nor live is
// read through; a write expecting exactly that copy goes live (default
// routing and a forced --live alike), and any other Expect (an older or
// newer version, or a create) stays E_STALE with nothing written.
func TestLiveBackend_StampOfALandedTask(t *testing.T) {
	cases := []struct {
		name      string
		forced    bool
		expect    func(landed store.Row) hlc.HLC
		wantStale bool
	}{
		{"default routing, expects the landed copy", false, func(l store.Row) hlc.HLC { return l.HLC }, false},
		{"forced live (fenced coordination write), expects the landed copy", true, func(l store.Row) hlc.HLC { return l.HLC }, false},
		{"expects another version", false, func(store.Row) hlc.HLC {
			return ltRow(t, schema.TableTasks, ltTaskA, 9, "x", false).HLC
		}, true},
		{"a create over the landed copy", true, func(store.Row) hlc.HLC { return hlc.HLC{} }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newLiveFixture(t)
			dir := f.b.top
			done := ltRow(t, schema.TableTasks, ltTaskA, 1, "done, not merged", false)
			rel := mainDataPath(live.RootFolder, schema.TableTasks+"/"+ltTaskA+rowFileExt)
			full := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, done.Doc, 0o600); err != nil {
				t.Fatal(err)
			}
			gitT(t, dir, "add", "-A")
			gitT(t, dir, "commit", "-q", "-m", "landed")
			gitT(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
			// This checkout's branch is older than origin/main: the row is
			// not in the work tree.
			gitT(t, dir, "reset", "-q", "--hard", "HEAD~1")
			got, err := f.b.Get(f.ctx, done.Table, done.ID)
			if err != nil || got.HLC.Compare(done.HLC) != 0 {
				t.Fatalf("the landed task is read through: %v", err)
			}
			ctx := f.ctx
			if c.forced {
				ctx = store.WithLiveChoice(ctx, store.LiveChoice{Mode: store.LiveOn, Table: done.Table})
			}
			stamp := ltRow(t, schema.TableTasks, ltTaskA, 2, "merged", false)
			err = f.b.Write(ctx, []store.Change{{Row: stamp, Expect: c.expect(got)}})
			if c.wantStale {
				if !errors.Is(err, store.ErrStale) {
					t.Fatalf("want E_STALE, got %v", err)
				}
				if f.liveHas(t, schema.TableTasks, ltTaskA) {
					t.Fatal("a stale write left a live row")
				}
				return
			}
			if err != nil {
				t.Fatalf("the stamp of a landed task writes, not E_STALE: %v", err)
			}
			if !f.liveHas(t, schema.TableTasks, ltTaskA) {
				t.Fatal("the stamp of a live kind goes live")
			}
			now, err := f.b.Get(f.ctx, done.Table, done.ID)
			if err != nil || now.HLC.Compare(stamp.HLC) != 0 || !strings.Contains(string(now.Doc), "merged") {
				t.Fatalf("Get reads the stamped copy: %v %s", err, now.Doc)
			}
		})
	}
}
