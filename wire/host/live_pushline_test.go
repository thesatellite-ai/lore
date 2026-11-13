package host

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/store"
)

// ltComment is a comments row id (a live kind with no lifecycle: it lands
// as soon as it is written).
const ltComment = "cmt_01a1a6841fc0784d92a62b0acf148b2c"

// TestFinishNeverLandsAndPushesOnlyWhenAsked (U-2, owner rule: nothing
// pushes without the person saying so): a run that writes live rows, one
// of which is due to land, ends without moving the remote's main in either
// setting. With live.autopush unset the remote gets nothing at all and the
// person is told once how writes go out; with live.autopush true the live
// ref is pushed (one "(live, pushed)") and the person is told once how to
// turn that off. A second run says neither line again.
func TestFinishNeverLandsAndPushesOnlyWhenAsked(t *testing.T) {
	for name, c := range map[string]struct {
		config   string
		wantLive bool
		wantSaid string
	}{
		"autopush unset": {config: "live:\n  max_age: 1h\n", wantSaid: liveAutopushOff},
		"autopush on":    {config: "live:\n  max_age: 1h\n  autopush: true\n", wantLive: true, wantSaid: liveAutopushOn},
	} {
		t.Run(name, func(t *testing.T) {
			clk := clock.NewFake(ltBase)
			f := newLiveFixtureWith(t, c.config, clk)
			f.s.release()
			top := f.s.top
			bare := filepath.Join(t.TempDir(), "r.git")
			gitT(t, top, "init", "-q", "--bare", "-b", "main", bare)
			gitT(t, top, "remote", "add", "origin", bare)
			gitT(t, top, "push", "-q", "origin", "main")
			gitT(t, top, "fetch", "-q", "origin")
			mainBefore := gitOutT(t, bare, "rev-parse", "refs/heads/main")
			run := func(id, title string) string {
				var out strings.Builder
				f.s.h.cfg.Stderr = &out
				r := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}}
				ctx := withRun(context.Background(), r)
				b := &liveBackend{inner: f.inner, top: top, folder: live.RootFolder}
				if id == ltTaskA { // a comment lands as soon as it is written
					if err := b.Write(ctx, []store.Change{{Row: ltRow(t, "comments", ltComment, 1, "a note", false)}}); err != nil {
						t.Fatal(err)
					}
				}
				// an open task past live.max_age is due for a copy to main
				if err := b.Write(ctx, []store.Change{{Row: ltRow(t, "tasks", id, 1, title, false)}}); err != nil {
					t.Fatal(err)
				}
				clk.Advance(2 * time.Hour)
				r.finish(ctx)
				return out.String()
			}
			got := run(ltTaskA, "open work")
			if strings.Contains(got, "landed on") {
				t.Fatalf("a write landed on main by itself:\n%s", got)
			}
			if after := gitOutT(t, bare, "rev-parse", "refs/heads/main"); after != mainBefore {
				t.Fatalf("the remote main moved from %s to %s without lore sync or git push:\n%s", mainBefore, after, got)
			}
			remoteLive := strings.TrimSpace(gitOutT(t, bare, "for-each-ref", "refs/lore/"))
			if (remoteLive != "") != c.wantLive {
				t.Fatalf("remote refs/lore/* = %q, want pushed=%v:\n%s", remoteLive, c.wantLive, got)
			}
			if n := strings.Count(got, "(live, pushed)"); (n == 1) != c.wantLive || n > 1 {
				t.Fatalf("'(live, pushed)' printed %d times:\n%s", n, got)
			}
			if strings.Count(got, c.wantSaid) != 1 {
				t.Fatalf("want the line %q once:\n%s", c.wantSaid, got)
			}
			if again := run(ltTaskB, "more work"); strings.Contains(again, c.wantSaid) {
				t.Fatalf("the autopush line was said again on the next write:\n%s", again)
			}
		})
	}
}

// gitOutT is gitT returning git's trimmed output.
func gitOutT(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: dir, Args: args, Env: []string{"GIT_CONFIG_NOSYSTEM=1"}})
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
