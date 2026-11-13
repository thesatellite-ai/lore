package host

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/store"
)

// TestReadNeverPushesWaitingLiveWrites (S-1): with live.autopush off a
// live write waits on this clone; a later read that fetches like sync
// (sync status: FetchAlways), and a dry run (marked read by around), end
// without pushing it, and so does a write that fetches like sync but is
// no push the person asked for (live compact or restore with nothing to
// rewrite): the remote gets no refs/lore/live. lore sync's run (the push
// asked for) does push, so the check bites.
func TestReadNeverPushesWaitingLiveWrites(t *testing.T) {
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

	// a live write with autopush off: it stays on this clone
	w := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}}
	ctx := withRun(context.Background(), w)
	b := &liveBackend{inner: f.inner, top: top, folder: live.RootFolder}
	if err := b.Write(ctx, []store.Change{{Row: ltRow(t, "comments", ltComment, 1, "a note", false)}}); err != nil {
		t.Fatal(err)
	}
	w.finish(ctx)
	if got := lsRemoteLive(t, bare); got != "" {
		t.Fatalf("a write with autopush off pushed refs/lore/live (%s):\n%s", got, errOut.String())
	}

	// a read that fetches like sync: nothing is pushed
	r := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways, read: true}
	rctx := withRun(context.Background(), r)
	if s := r.session(rctx, top); s == nil {
		t.Fatal("no live session for the read")
	}
	r.finish(rctx)
	if got := lsRemoteLive(t, bare); got != "" {
		t.Fatalf("a read pushed the waiting live write (%s):\n%s", got, errOut.String())
	}

	// a write that fetches like sync but is no push the person asked for
	// (live compact or restore with nothing to rewrite): nothing is pushed
	c := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways}
	cctx := withRun(context.Background(), c)
	if cs := c.session(cctx, top); cs == nil {
		t.Fatal("no live session for the compact")
	}
	c.finish(cctx)
	if got := lsRemoteLive(t, bare); got != "" {
		t.Fatalf("a run that fetches like sync but asked for no push pushed the waiting live write (%s):\n%s", got, errOut.String())
	}

	// the same run as lore sync (FetchAlways, the push asked for) does push
	s := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways, asked: pushAsked["sync.run"]}
	sctx := withRun(context.Background(), s)
	if ss := s.session(sctx, top); ss == nil {
		t.Fatal("no live session for the sync")
	}
	s.finish(sctx)
	if got := lsRemoteLive(t, bare); got == "" {
		t.Fatalf("lore sync's run did not push the waiting live write, so the read check proves nothing:\n%s", errOut.String())
	}
}

// lsRemoteLive is the remote's refs/lore/live object id, "" when absent.
func lsRemoteLive(t *testing.T, bare string) string {
	t.Helper()
	out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Args: []string{"--git-dir", bare, "for-each-ref", "--format=%(objectname)", live.RefLive}})
	if err != nil {
		t.Fatalf("git for-each-ref: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}
