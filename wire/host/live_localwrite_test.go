package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// liveCountingSSH is a fake ssh that logs the command git asks the host to
// run (git-upload-pack is a fetch, git-receive-pack a push) and runs it.
const liveCountingSSH = "#!/bin/sh\nfor a; do cmd=$a; done\necho \"$cmd\" >> \"$FAKE_SSH_LOG\"\nexec sh -c \"$cmd\"\n"

// devco L-12-perf: on a slow remote the first write after a pause fetched
// (live.fetch_ttl had run out) though it only writes refs/lore/live here.
// Now a write does no network I/O at all, however old the last fetch is;
// a read that is due still fetches, a write the person passed --push to
// still fetches, and lore sync makes one fetch and one push.
func TestLocalWriteDoesNoNetworkIO(t *testing.T) {
	clk := clock.NewFake(ltBase)
	f := newLiveFixtureWith(t, "live:\n  autopush: false\n", clk)
	f.s.release()
	top := f.s.top
	bare := filepath.Join(t.TempDir(), "r.git")
	gitT(t, top, "init", "-q", "--bare", "-b", "main", bare)
	gitT(t, top, "remote", "add", "origin", "ssh://fakehost"+filepath.ToSlash(bare))
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("GIT_SSH_COMMAND", filepath.ToSlash(fakeexec.Write(t, filepath.Join(dir, "ssh"), liveCountingSSH, 0o755)))
	t.Setenv("GIT_SSH_VARIANT", "simple")
	t.Setenv("FAKE_SSH_LOG", log)
	gitT(t, top, "push", "-q", "origin", "main")
	calls := func() (fetches, pushes int) {
		b, _ := os.ReadFile(log) // no file yet: no call
		_ = os.Remove(log)
		return strings.Count(string(b), "git-upload-pack"), strings.Count(string(b), "git-receive-pack")
	}
	calls()
	var errOut strings.Builder
	f.s.h.cfg.Stderr = &errOut
	n := 0
	write := func(r *liveRun) {
		t.Helper()
		n++
		ctx := withRun(context.Background(), r)
		b := &liveBackend{inner: f.inner, top: top, folder: live.RootFolder}
		if err := b.Write(ctx, []store.Change{{Row: ltRow(t, "comments", fmt.Sprintf("cmt_01a1a6841fc0784d92a62b0acf148b%02x", n), n, "note", false)}}); err != nil {
			t.Fatal(err)
		}
		r.finish(ctx)
	}
	open := func(r *liveRun) {
		t.Helper()
		ctx := withRun(context.Background(), r)
		if r.session(ctx, top) == nil {
			t.Fatal("no live session")
		}
		r.finish(ctx)
	}
	run := func() *liveRun { return &liveRun{h: f.s.h, sessions: map[string]*liveSession{}} }

	// never fetched: a write stays local
	write(run())
	if fe, pu := calls(); fe+pu != 0 {
		t.Fatalf("a local write made %d fetch(es) and %d push(es), want none:\n%s", fe, pu, errOut.String())
	}
	// a read that is due fetches (the counter bites)
	rd := run()
	rd.read = true
	open(rd)
	if fe, _ := calls(); fe != 1 {
		t.Fatalf("a due read made %d fetch(es), want 1", fe)
	}
	// long after that fetch (a pause): a write still stays local
	clk.Advance(2 * time.Hour)
	write(run())
	if fe, pu := calls(); fe+pu != 0 {
		t.Fatalf("a write after a pause made %d fetch(es) and %d push(es), want none:\n%s", fe, pu, errOut.String())
	}
	// a write the person passed --push to fetches when the fetch is due
	pr := run()
	pr.netAsked = true
	write(pr)
	if fe, _ := calls(); fe != 1 {
		t.Fatalf("a --push write made %d fetch(es), want 1", fe)
	}
	// lore sync: one fetch and one push for the waiting writes
	sr := run()
	sr.kind, sr.asked = live.FetchAlways, pushAsked["sync.run"]
	open(sr)
	if fe, pu := calls(); fe != 1 || pu != 1 {
		t.Fatalf("lore sync made %d fetch(es) and %d push(es), want 1 and 1:\n%s", fe, pu, errOut.String())
	}
	if lsRemoteLive(t, bare) == "" {
		t.Fatal("lore sync did not deliver the local writes")
	}
}
