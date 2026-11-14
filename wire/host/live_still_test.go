package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/internal/untouchedwalk"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/op"
	opscheck "github.com/thesatellite-ai/lore/ops/check"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	"github.com/thesatellite-ai/lore/store"
)

// clearCIMarkers empties every CI variable for the test, so a runner's own
// CI=true does not decide what the test checks.
func clearCIMarkers(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "JENKINS_URL", "TF_BUILD", "BITBUCKET_BUILD_NUMBER"} {
		t.Setenv(k, "")
	}
}

// TestStillRunsAreDryRunsAndCIForms (devco K-9, K-15): the runs that must
// leave .git as it was are the dry runs, lore check leaks --ci, render's
// read forms, plain lore fix in a 0.1.17-format repo, and any read on a CI
// runner; a plain check leaks, a write and a read off CI are not.
func TestStillRunsAreDryRunsAndCIForms(t *testing.T) {
	read := op.Spec{Name: "memory.list", Permission: op.PermRead, Access: op.AccessRead}
	leaks := op.Spec{Name: opscheck.OpLeaks, Permission: op.PermRead, Access: op.AccessRead}
	render := op.Spec{Name: opsrender.OpRun, Permission: op.PermWrite, Access: op.AccessRead}
	fix := op.Spec{Name: opsfix.OpRun, Permission: op.PermWrite, Access: op.AccessWrite}
	write := op.Spec{Name: "memory.add", Permission: op.PermWrite, Access: op.AccessWrite}
	next := op.Spec{Name: "task.next", Permission: op.PermWrite, Access: op.AccessWrite}
	body := func(s string) op.Decoder { return op.JSONDecoder{Body: []byte(s)} }
	cases := []struct {
		name      string
		run       Run
		ciEnv     bool
		still, ci bool
	}{
		{"check leaks --ci", Run{Entry: specEntry{spec: leaks}, Decoder: body(`{"ci":true}`)}, false, true, true},
		{"check leaks (commit hook)", Run{Entry: specEntry{spec: leaks}, Decoder: body(`{"staged":true}`)}, false, false, false},
		{"render --check", Run{Entry: specEntry{spec: render}, Decoder: body(`{"check":true}`)}, false, true, true},
		{"render", Run{Entry: specEntry{spec: render}, Decoder: body(`{}`)}, false, false, false},
		{"fix --dry-run", Run{Entry: specEntry{spec: fix}, Decoder: body(`{"dry_run":true}`)}, false, true, false},
		{"task next --dry-run reads teammates' live rows", Run{Entry: specEntry{spec: next}, Decoder: body(`{"dry_run":true}`)}, false, false, false},
		{"task next --dry-run on a CI runner", Run{Entry: specEntry{spec: next}, Decoder: body(`{"dry_run":true}`)}, true, true, true},
		{"fix in a 0.1.17-format repo", Run{Entry: specEntry{spec: fix}, Decoder: body(`{}`), OldRows: true}, false, true, false},
		{"fix", Run{Entry: specEntry{spec: fix}, Decoder: body(`{}`)}, false, false, false},
		{"a read on a CI runner", Run{Entry: specEntry{spec: read}, Decoder: body(`{}`)}, true, true, true},
		{"a read off CI", Run{Entry: specEntry{spec: read}, Decoder: body(`{}`)}, false, false, false},
		{"a write on a CI runner", Run{Entry: specEntry{spec: write}, Decoder: body(`{}`)}, true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearCIMarkers(t)
			if c.ciEnv {
				t.Setenv("GITHUB_ACTIONS", "true")
			}
			if got := stillRun(&c.run); got != c.still {
				t.Errorf("stillRun = %v, want %v", got, c.still)
			}
			if got := ciRun(&c.run); got != c.ci {
				t.Errorf("ciRun = %v, want %v", got, c.ci)
			}
		})
	}
}

// TestStillSessionLeavesGitAsItWas (devco K-9, K-15): the remote holds
// refs/lore/live and a fetch is due (a sync-like run), yet a still run's
// session takes no live lock, fetches nothing and integrates nothing: the
// clone's .git, .git/lore included, is byte-identical afterwards. The same
// run without still writes there, so the check bites.
func TestStillSessionLeavesGitAsItWas(t *testing.T) {
	clk := clock.NewFake(ltBase)
	f := newLiveFixtureWith(t, "live:\n  autopush: false\n", clk)
	f.s.release()
	top := f.s.top
	bare := filepath.Join(t.TempDir(), "r.git")
	gitT(t, top, "init", "-q", "--bare", "-b", "main", bare)
	gitT(t, top, "remote", "add", "origin", bare)
	gitT(t, top, "push", "-q", "origin", "main")
	var errOut strings.Builder
	f.s.h.cfg.Stderr = &errOut

	// a live write, pushed by lore sync's run: the remote has refs/lore/live
	w := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways, asked: pushAsked["sync.run"]}
	ctx := withRun(context.Background(), w)
	b := &liveBackend{inner: f.inner, top: top, folder: live.RootFolder}
	if err := b.Write(ctx, []store.Change{{Row: ltRow(t, "comments", ltComment, 1, "a note", false)}}); err != nil {
		t.Fatal(err)
	}
	w.finish(ctx)
	if lsRemoteLive(t, bare) == "" {
		t.Fatalf("the setup push did not reach the remote:\n%s", errOut.String())
	}
	// a clone that never fetched lore's refs (as CI's checkout)
	gitT(t, top, "update-ref", "-d", live.RefLive)
	if err := os.RemoveAll(filepath.Join(top, ".git", liveStateDirName)); err != nil {
		t.Fatal(err)
	}

	before, err := untouchedwalk.Tree(filepath.Join(top, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	r := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways, read: true, still: true, ci: true}
	rctx := withRun(context.Background(), r)
	if s := r.session(rctx, top); s == nil {
		t.Fatal("no live session for the still run")
	}
	r.finish(rctx)
	after, err := untouchedwalk.Tree(filepath.Join(top, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := untouchedwalk.Diff(before, after); diff != "" {
		t.Fatalf("a still run wrote into .git:\n%s\n%s", diff, errOut.String())
	}

	// the same run without still fetches and writes .git/lore
	n := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways, read: true}
	nctx := withRun(context.Background(), n)
	n.session(nctx, top)
	n.finish(nctx)
	again, err := untouchedwalk.Tree(filepath.Join(top, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if untouchedwalk.Diff(before, again) == "" {
		t.Fatal("a read that is not still wrote nothing into .git either, so the still check proves nothing")
	}
}
