package host

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRewrittenRemoteRefusesThenRestores (b-flow4, F8): once the remote
// live ref is deleted, the next run's write is refused (E_READ_ONLY) with
// the backup ref it names existing in the clone; `lore live restore`
// pushes the rows back, and the run after it writes again.
func TestRewrittenRemoteRefusesThenRestores(t *testing.T) {
	f := newLiveFixture(t)
	f.s.release()
	top := f.s.top
	bare := filepath.Join(t.TempDir(), "r.git")
	gitT(t, top, "init", "-q", "--bare", "-b", "main", bare)
	gitT(t, top, "remote", "add", "origin", bare)
	gitT(t, top, "push", "-q", "origin", "main")
	// run is one command: a fresh run whose session fetches (FetchAlways)
	// and pushes what it wrote, as a push the person asked for does
	// (asked: live.autopush is off by default, and fetching like sync is
	// not asking for a push, S-1).
	run := func(rows ...store.Row) error {
		t.Helper()
		r := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways, asked: true}
		ctx := withRun(context.Background(), r)
		defer r.finish(ctx)
		b := &liveBackend{inner: f.inner, top: top, folder: live.RootFolder}
		s := b.session(ctx)
		// LiveHook's pre-run refusal of a write (the path every CLI write takes).
		if err := f.s.h.refuse(ctx, s, op.Spec{Name: "task.add", Access: op.AccessWrite}); err != nil {
			return err
		}
		changes := make([]store.Change, 0, len(rows))
		for _, row := range rows {
			changes = append(changes, store.Change{Row: row})
		}
		return b.Write(ctx, changes)
	}
	if err := run(ltRow(t, schema.TableTasks, ltTaskA, 1, "before", false)); err != nil {
		t.Fatal(err)
	}
	gitT(t, bare, "update-ref", "-d", live.RefLive) // someone deleted the remote live ref
	err := run(ltRow(t, schema.TableTasks, ltTaskB, 2, "after", false))
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.ReadOnly {
		t.Fatalf("write after the remote ref was deleted: %v, want E_READ_ONLY", err)
	}
	backup := backupRefOf(e.Unwrap())
	if backup == "" || !strings.Contains(e.Message, backup) {
		t.Fatalf("the refusal does not name the backup ref: %q", e.Message)
	}
	if _, _, rerr := f.s.repo.ResolveRef(context.Background(), backup); rerr != nil {
		t.Fatalf("the named backup %s does not exist: %v", backup, rerr)
	}
	// lore live restore, then a write goes through again.
	r := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways}
	ctx := withRun(context.Background(), r)
	ref, err := f.s.h.liveRef(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, rerr := ref.Restore(ctx); rerr != nil {
		t.Fatalf("restore: %v", rerr)
	}
	r.finish(ctx)
	out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top, Args: []string{"ls-remote", bare, live.RefLive}})
	if err != nil || !strings.Contains(string(out), live.RefLive) {
		t.Fatalf("restore did not put the live ref back: %q %v", out, err)
	}
	if err := run(ltRow(t, schema.TableTasks, ltTaskB, 3, "after restore", false)); err != nil {
		t.Fatalf("write after restore: %v", err)
	}
}
