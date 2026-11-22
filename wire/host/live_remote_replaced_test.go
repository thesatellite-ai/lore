package host

// live_remote_replaced_test.go — #136 (devco D-913): the code remote was
// replaced by a new empty repository at the same url (or its live ref was
// deleted). Writes that are not live go on, live writes stop with a hint
// that names `lore live restore --fresh` (pushes nothing) before `lore
// live restore` (publishes), lore sync reports instead of failing, and
// --fresh starts a new live ref without ever sending the old rows.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

func TestRemoteReplacedStopsOnlyLiveWrites(t *testing.T) {
	f := newLiveFixture(t)
	f.s.release()
	top := f.s.top
	bare := filepath.Join(t.TempDir(), "r.git")
	gitT(t, top, "init", "-q", "--bare", "-b", "main", bare)
	gitT(t, top, "remote", "add", "origin", bare)
	gitT(t, top, "push", "-q", "origin", "main")
	// run is one command named opName: a fresh run whose session fetches
	// and pushes what it wrote (a push the person asked for).
	run := func(opName string, rows ...store.Row) error {
		t.Helper()
		r := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways, asked: true}
		ctx := withRun(context.Background(), r)
		defer r.finish(ctx)
		b := &liveBackend{inner: f.inner, top: top, folder: live.RootFolder}
		s := b.session(ctx)
		if err := f.s.h.refuse(ctx, s, op.Spec{Name: opName, Access: op.AccessWrite}); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		changes := make([]store.Change, 0, len(rows))
		for _, row := range rows {
			changes = append(changes, store.Change{Row: row})
		}
		return b.Write(ctx, changes)
	}
	remoteLive := func() string {
		t.Helper()
		out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top, Args: []string{"ls-remote", bare, live.RefLive}})
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if err := run("task.add", ltRow(t, schema.TableTasks, ltTaskA, 1, "old private task", false)); err != nil {
		t.Fatal(err)
	}
	if remoteLive() == "" {
		t.Fatal("setup: the live ref was not pushed")
	}
	// the remote is replaced by a new empty repository at the same path
	gitT(t, top, "init", "-q", "--bare", "-b", "main", bare+".new")
	if err := os.Rename(bare, bare+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(bare+".new", bare); err != nil {
		t.Fatal(err)
	}
	gitT(t, top, "push", "-q", "origin", "main")

	// a write that is not live goes on
	if err := run("memory.add"); err != nil {
		t.Fatalf("a write that is not live was refused: %v", err)
	}
	// a live write stops, naming the choice that pushes nothing first
	err := run("task.add", ltRow(t, schema.TableTasks, ltTaskB, 2, "new task", false))
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.ReadOnly {
		t.Fatalf("live write: %v, want E_READ_ONLY", err)
	}
	fresh, restore := strings.Index(e.Hint, "`lore live restore --fresh`"), strings.Index(e.Hint, "run `lore live restore --publish`")
	if fresh < 0 || restore < 0 || fresh > restore || !strings.Contains(e.Message, "new repository") {
		t.Fatalf("message %q / hint %q: want the new-repository reason and --fresh named before restore", e.Message, e.Hint)
	}
	// lore sync reports instead of failing, and sends nothing
	r := &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways, asked: true}
	ctx := withRun(context.Background(), r)
	out, err := (liveSync{h: f.s.h}).SyncRun(ctx, opssync.RunIn{})
	r.finish(ctx)
	if err != nil || !strings.Contains(strings.Join(out.Lines, "\n"), "live rows not sent") {
		t.Fatalf("sync: %v %q, want a report naming the live rows not sent", err, out.Lines)
	}
	if got := remoteLive(); got != "" {
		t.Fatalf("sync pushed the old live rows to the new remote: %q", got)
	}
	// a plain lore live restore refuses (devco D-914): publishing the old
	// rows on the new remote takes the explicit --publish
	r = &liveRun{h: f.s.h, sessions: map[string]*liveSession{}, kind: live.FetchAlways}
	ctx = withRun(context.Background(), r)
	ref, err := f.s.h.liveRef(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, rerr := ref.Restore(ctx, false); errs.CodeOf(rerr) != errs.Refused {
		t.Fatalf("plain restore on the new remote: %v, want E_REFUSED", rerr)
	}
	if got := remoteLive(); got != "" {
		t.Fatalf("the refused restore pushed the old live rows: %q", got)
	}
	// lore live restore --fresh: nothing pushed, the old rows kept in a backup
	res, err := ref.StartFresh(ctx)
	r.finish(ctx)
	if err != nil || !res.Fresh || !strings.HasPrefix(res.Backup, live.RefBackupPrefix) {
		t.Fatalf("start fresh: %+v %v", res, err)
	}
	if _, ok, rerr := f.s.repo.ResolveRef(context.Background(), res.Backup); rerr != nil || !ok {
		t.Fatalf("the backup %s is missing: %v", res.Backup, rerr)
	}
	if got := remoteLive(); got != "" {
		t.Fatalf("--fresh pushed: %q", got)
	}
	// the next live write starts a new live ref holding only the new row
	if err := run("task.add", ltRow(t, schema.TableTasks, ltTaskB, 3, "new task", false)); err != nil {
		t.Fatalf("live write after --fresh: %v", err)
	}
	tip := strings.Fields(remoteLive())
	if len(tip) == 0 {
		t.Fatal("the new live ref was not pushed")
	}
	files, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top, Args: []string{"ls-tree", "-r", "--name-only", tip[0]}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files), ltTaskA) || !strings.Contains(string(files), ltTaskB) {
		t.Fatalf("the new remote's live ref holds %q: want only the new task", files)
	}
}
