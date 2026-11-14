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

// TestSyncHoldsLiveWritesOfARemovedPerson (F-9): a person the org removed
// made a live write before the sync that brings the removal; that sync
// (the push asked for) keeps refs/lore/live on this clone and says why in
// one line. Once the hold is gone (an admin restored them) the same sync
// pushes, so the check bites.
func TestSyncHoldsLiveWritesOfARemovedPerson(t *testing.T) {
	clk := clock.NewFake(ltBase)
	f := newLiveFixtureWith(t, "live:\n  autopush: false\n", clk)
	f.s.release()
	top := f.s.top
	bare := filepath.Join(t.TempDir(), "r.git")
	gitT(t, top, "init", "-q", "--bare", "-b", "main", bare)
	gitT(t, top, "remote", "add", "origin", bare)
	gitT(t, top, "push", "-q", "origin", "main")
	gitT(t, top, "fetch", "-q", "origin")
	var errOut strings.Builder
	f.s.h.cfg.Stderr = &errOut
	const reason = "you are marked as left in acme since 2026-01-04"
	left := true
	f.s.h.cfg.PushHold = func(context.Context) (time.Time, string, bool) {
		return ltBase.Add(-24 * time.Hour), reason, left
	}

	w := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}}
	ctx := withRun(context.Background(), w)
	b := &liveBackend{inner: f.inner, top: top, folder: live.RootFolder}
	if err := b.Write(ctx, []store.Change{{Row: ltRow(t, "comments", ltComment, 1, "after leaving", false)}}); err != nil {
		t.Fatal(err)
	}
	w.finish(ctx)

	sync := func() {
		s := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways, asked: pushAsked["sync.run"]}
		sctx := withRun(context.Background(), s)
		if ss := s.session(sctx, top); ss == nil {
			t.Fatal("no live session for the sync")
		}
		s.finish(sctx)
	}
	sync()
	if got := lsRemoteLive(t, bare); got != "" {
		t.Fatalf("the sync pushed a removed person's live write (%s):\n%s", got, errOut.String())
	}
	if !strings.Contains(errOut.String(), reason) || !strings.Contains(errOut.String(), "kept on this machine") {
		t.Fatalf("the held push did not say why:\n%s", errOut.String())
	}

	left = false
	sync()
	if got := lsRemoteLive(t, bare); got == "" {
		t.Fatalf("with no hold the sync did not push, so the check proves nothing:\n%s", errOut.String())
	}
}
