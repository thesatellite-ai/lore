package host

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestPrePushLeakCheckRunsUnderTheHookRun: the pre-push F16 check reads
// rows through the app, so it runs with the hook's live run in its ctx
// (the repo store's live overlay then finds a session: live-only rows are
// seen, and no false "v0.1.17 live rows not visible yet" note is printed
// in `git push`). A refused push drops the live lock and pushes nothing
// of the run; an allowed one finishes the run (the waiting live write is
// pushed).
func TestPrePushLeakCheckRunsUnderTheHookRun(t *testing.T) {
	cases := map[string]struct {
		refuse   bool
		wantLive bool
	}{
		"refused": {refuse: true, wantLive: false},
		"allowed": {refuse: false, wantLive: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLiveFixture(t)
			if err := f.b.Write(f.ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 1, "open work", false)}}); err != nil {
				t.Fatal(err)
			}
			f.s.release() // the fixture's run is over; the hook takes the lock itself
			top := f.s.top
			bare := filepath.Join(t.TempDir(), "r.git")
			gitT(t, top, "init", "-q", "--bare", bare)
			gitT(t, top, "remote", "add", "origin", bare)
			t.Chdir(top)

			sawRun := false
			h := f.s.h
			h.cfg.LeakCheck = func(ctx context.Context, _ string, _ map[string][]byte) error {
				if r := runFrom(ctx); r != nil && r.session(ctx, top) != nil {
					sawRun = true
				}
				if c.refuse {
					return errs.New(errs.LeakBlocked, "test refusal")
				}
				return nil
			}
			head, _, err := f.s.repo.ResolveCommit(f.ctx, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			in := "refs/heads/main " + head + " refs/heads/main " + strings.Repeat("0", len(head)) + "\n"
			err = h.prePush(context.Background(), strings.NewReader(in), []string{"origin", bare})
			if c.refuse != errs.Has(err, errs.LeakBlocked) {
				t.Fatalf("prePush: %v (refuse=%v)", err, c.refuse)
			}
			if !sawRun {
				t.Fatal("the leak check ran without the hook's live session in its ctx")
			}
			// The lock is free again (a refusal must not keep it).
			l, lerr := tryLiveLock(filepath.Join(f.s.stateDir, liveLockFile))
			if lerr != nil {
				t.Fatalf("live lock still held after prePush: %v", lerr)
			}
			if rerr := l.release(); rerr != nil {
				t.Fatal(rerr)
			}
			out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top, Args: []string{"ls-remote", bare, "refs/lore/*"}})
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(out), "refs/lore/live"); got != c.wantLive {
				t.Fatalf("remote live ref present=%v, want %v (ls-remote: %q)", got, c.wantLive, out)
			}
		})
	}
}
