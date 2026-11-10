package host

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/store"
)

// ltComment is a comments row id (a live kind with no lifecycle: it lands
// as soon as it is written).
const ltComment = "cmt_01a1a6841fc0784d92a62b0acf148b2c"

// TestFinishReportsOnePushWhenAWriteLands (b-flow4): a write that lands in
// the same run pushes the live ref twice (the write, then the landing's
// copy); `lore live: (live, pushed)` is printed once, as in v0.1.17, and
// the landing line still follows it.
func TestFinishReportsOnePushWhenAWriteLands(t *testing.T) {
	clk := clock.NewFake(ltBase)
	f := newLiveFixtureWith(t, "live:\n  max_age: 1h\n", clk)
	f.s.release()
	top := f.s.top
	bare := filepath.Join(t.TempDir(), "r.git")
	gitT(t, top, "init", "-q", "--bare", "-b", "main", bare)
	gitT(t, top, "remote", "add", "origin", bare)
	gitT(t, top, "push", "-q", "origin", "main")
	gitT(t, top, "fetch", "-q", "origin")
	var out strings.Builder
	f.s.h.cfg.Stderr = &out
	r := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}}
	ctx := withRun(context.Background(), r)
	b := &liveBackend{inner: f.inner, top: top, folder: live.RootFolder}
	if err := b.Write(ctx, []store.Change{{Row: ltRow(t, "comments", ltComment, 1, "a note", false)}}); err != nil {
		t.Fatal(err)
	}
	// An open task past live.max_age is copied to main by the landing pass
	// and stays live: its live file changes, so the live ref is pushed a
	// second time after landing.
	if err := b.Write(ctx, []store.Change{{Row: ltRow(t, "tasks", ltTaskA, 1, "open work", false)}}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(2 * time.Hour)
	r.finish(ctx)
	got := out.String()
	if n := strings.Count(got, "(live, pushed)"); n != 1 {
		t.Fatalf("'(live, pushed)' printed %d times:\n%s", n, got)
	}
	if !strings.Contains(got, "landed on main") {
		t.Fatalf("the write did not land (the double-push path was not reached):\n%s", got)
	}
}
