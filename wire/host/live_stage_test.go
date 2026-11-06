package host

// Port of saas/cmd/cli live_core_test.go TestLiveBackend_CollectorStagesOnly
// (later 603) onto wire/host's wire.LiveAtomic (live_stage.go): a
// coordination write's live rows are staged on the local live ref only;
// they go out in the caller's one atomic push (AtomicUpdate), and a write
// whose push is not confirmed is rolled back, never pushed on its own.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// remoteLive is the remote's refs/lore/live ("" = absent).
func remoteLive(t *testing.T, dir, bare string) string {
	t.Helper()
	out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: dir, Args: []string{"ls-remote", bare, live.RefLive}})
	if err != nil {
		t.Fatal(err)
	}
	sha, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	return sha
}

// TestLiveAtomic_StagesOnly: after AtomicMark, a live write moves only the
// local live ref (the remote and the branch copy are untouched) and
// AtomicUpdate names it as a fast-forward of refs/lore/live. Then:
// rollback puts the local ref back to the mark and the run's finish pushes
// nothing; an open mark at finish (the atomic push never ran) is undone
// the same way; a confirmed push leaves the remote at the staged commit.
func TestLiveAtomic_StagesOnly(t *testing.T) {
	cases := map[string]struct {
		end        string // "rollback", "open", "confirm"
		wantRemote bool
	}{
		"rolled back":          {end: "rollback"},
		"never pushed: finish": {end: "open"},
		"confirmed":            {end: "confirm", wantRemote: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLiveFixture(t)
			f.s.release()
			top := f.s.top
			bare := filepath.Join(t.TempDir(), "r.git")
			gitT(t, top, "init", "-q", "--bare", bare)
			gitT(t, top, "remote", "add", "origin", bare)
			r := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}}
			ctx := withRun(context.Background(), r)
			b := &liveBackend{inner: f.inner, top: top, folder: live.RootFolder}
			s := b.session(ctx)
			if s == nil {
				t.Fatal("no live session")
			}
			if remote, ok := b.AtomicRemote(ctx); !ok || remote != "origin" {
				t.Fatalf("AtomicRemote = %q %v, want origin", remote, ok)
			}
			if err := b.AtomicMark(ctx); err != nil {
				t.Fatal(err)
			}
			before, _ := s.ref.Snapshot(ctx)
			claim := ltRow(t, schema.TableTasks, ltTaskA, 1, "claimed", false)
			claimID, err := ids.ParseAny(ltTaskA)
			if err != nil {
				t.Fatal(err)
			}
			goesLive, err := b.AtomicRoutesLive(ctx, claim.Table, claimID)
			if err != nil || !goesLive {
				t.Fatalf("a new task routes live: %v %v", goesLive, err)
			}
			if err := b.Write(ctx, []store.Change{{Row: claim}}); err != nil {
				t.Fatal(err)
			}
			staged, _ := s.ref.Snapshot(ctx)
			if staged.Local == before.Local || staged.Local == "" {
				t.Fatal("the write is staged on the local live ref")
			}
			if remoteLive(t, top, bare) != "" {
				t.Fatal("a staged write is not pushed on its own")
			}
			if _, gerr := f.inner.Get(ctx, claim.Table, claim.ID); gerr == nil {
				t.Fatal("a live row is not written to the branch data")
			}
			u, err := b.AtomicUpdate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if u.Src != staged.Local || u.Dst != live.RefLive || u.Mode != gitremote.ModeFastForward {
				t.Fatalf("AtomicUpdate = %+v, want a fast-forward of %s to %s", u, live.RefLive, staged.Local)
			}
			switch c.end {
			case "rollback":
				if err := b.AtomicRollback(ctx); err != nil {
					t.Fatal(err)
				}
				if back, _ := s.ref.Snapshot(ctx); back.Local != before.Local {
					t.Fatalf("rollback: local %q, want the mark %q", back.Local, before.Local)
				}
			case "confirm":
				gitT(t, top, "push", "-q", bare, u.Src+":"+u.Dst) // the caller's atomic push
				if err := b.AtomicConfirm(ctx, u.Src); err != nil {
					t.Fatal(err)
				}
			}
			r.finish(ctx)
			got := remoteLive(t, top, bare)
			if c.wantRemote != (got != "") || (c.wantRemote && got != staged.Local) {
				t.Fatalf("remote live ref %q after the run (want present=%v at %s)", got, c.wantRemote, staged.Local)
			}
			if !c.wantRemote {
				if after, _ := s.ref.Snapshot(ctx); after.Local != before.Local {
					t.Fatalf("an unconfirmed write is undone at finish: local %q, mark %q", after.Local, before.Local)
				}
			}
		})
	}
}
