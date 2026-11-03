package live

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/gitremote/gitremotetest"
)

// newRef opens the live ref of a test clone with a fixed clock.
func newRef(t *testing.T, c *gitremotetest.Clone, remote gitremote.Remote) *Ref {
	t.Helper()
	return &Ref{
		Repo: c.Repo, Name: RefLive, Remote: remote, RemoteName: gitremotetest.RemoteName,
		StateDir: filepath.Join(c.Dir, ".git", "lore"), Clock: clock.NewFake(t0),
		Signature:  gitref.Signature{Name: c.Name, Email: c.Name + "@example.test"},
		Strategies: strategies, IgnoreKeys: []string{"updated_at"}, PushRetries: 5,
	}
}

func row(id, title, prio string) map[string]any {
	return map[string]any{"id": id, "title": title, "priority": prio}
}

func write(t *testing.T, r *Ref, rows map[RowKey]File) WriteResult {
	t.Helper()
	res, err := r.Write(context.Background(), WriteRequest{Folder: RootFolder, Rows: rows, BinaryVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func put(id string, f File) map[RowKey]File { return map[RowKey]File{{Table: "tasks", ID: id}: f} }

func mustPush(t *testing.T, r *Ref) PushOutcome {
	t.Helper()
	out, err := r.Push(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !out.Pushed() {
		t.Fatalf("push: %+v", out)
	}
	return out
}

func readRowAt(t *testing.T, r *Ref, rev, id string) (File, bool) {
	t.Helper()
	tr, err := r.ReadTree(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := tr.Rows(RootFolder)
	f, ok := rows[RowKey{Table: "tasks", ID: id}]
	return f, ok
}

func commitCount(t *testing.T, dir, rev string) int {
	t.Helper()
	return len(strings.Split(gitremotetest.Git(t, dir, "rev-list", rev), "\n"))
}

// F2 steps 3–4 and C2: the first write is one parentless commit with the
// ref's own files; the same write again commits nothing.
func TestWriteFirstIsParentlessAndRepeatIsNoOp(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	f := NewVersion(nil, row("tsk_1", "first", "low"), t0, "act_a")
	res := write(t, r, put("tsk_1", f))
	if !res.Changed || res.Old != "" {
		t.Fatalf("first write: %+v", res)
	}
	if n := commitCount(t, a.Dir, RefLive); n != 1 {
		t.Fatalf("first write must be one parentless commit, got %d", n)
	}
	tr, _ := r.ReadTree(context.Background(), res.New)
	if _, ok := tr[EpochPath]; !ok || tr.Epoch() != FirstEpoch {
		t.Fatal("first write creates _live/epoch")
	}
	if m, ok, _ := tr.Meta(); !ok || m.MinLoreVersion != MinWriterVersion {
		t.Fatalf("first write creates _live/meta.json: %+v", m)
	}
	if msg := gitremotetest.Git(t, a.Dir, "log", "-1", "--format=%s", RefLive); msg != "lore: live 1 row (_root)" {
		t.Fatalf("fixed message: %q", msg)
	}
	// Same body again (only updated_at moved, a new hlc): nothing to commit.
	again := NewVersion(&f, map[string]any{"id": "tsk_1", "title": "first", "priority": "low", "updated_at": "later"}, t0, "act_a")
	if res2 := write(t, r, put("tsk_1", again)); res2.Changed {
		t.Fatal("the same write twice must be a no-op")
	}
}

// E1 / F2 step 6: two clones write different fields of one row at once;
// the loser merges onto the winner's tip and the remote gains exactly two
// commits, no merge commit, and both edits survive.
func TestPushRaceMergesOntoRemoteTip(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	ra, rb := newRef(t, a, a.Remote), newRef(t, b, b.Remote)
	base := NewVersion(nil, row("tsk_1", "shared", "low"), t0, "act_a")
	write(t, ra, put("tsk_1", base))
	mustPush(t, ra)
	if err := rb.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if res, err := rb.Integrate(context.Background()); err != nil || res.Action != IntegrateTookRemote {
		t.Fatalf("integrate: %+v %v", res, err)
	}
	before := commitCount(t, w.OriginDir, RefLive)
	write(t, ra, put("tsk_1", NewVersion(&base, row("tsk_1", "renamed by a", "low"), t0, "act_a")))
	write(t, rb, put("tsk_1", NewVersion(&base, row("tsk_1", "shared", "urgent"), t0, "act_b")))
	mustPush(t, ra)
	out := mustPush(t, rb)
	if out.Attempts != 2 {
		t.Fatalf("the loser pushes twice (reject, merge, push): %+v", out)
	}
	if got := commitCount(t, w.OriginDir, RefLive) - before; got != 2 {
		t.Fatalf("two writes add exactly two commits, added %d", got)
	}
	if parents := strings.Fields(gitremotetest.Git(t, w.OriginDir, "log", "-1", "--format=%P", RefLive)); len(parents) != 1 {
		t.Fatalf("no merge commit: parents %v", parents)
	}
	f, _ := readRowAt(t, rb, w.OriginRef(RefLive), "tsk_1")
	if f.Body["title"] != "renamed by a" || f.Body["priority"] != "urgent" {
		t.Fatalf("both edits must survive: %v", f.Body)
	}
}

// E2 / F3: an offline write stays on the local ref (the queue) with a
// pending entry; a teammate writes meanwhile; once online, nothing is lost
// and the pending list empties.
func TestOfflineWriteIsQueuedThenPushed(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	fault := gitremote.NewFaultRemote(a.Remote)
	ra, rb := newRef(t, a, fault), newRef(t, b, b.Remote)
	fault.SetOffline(true)
	write(t, ra, put("tsk_a", NewVersion(nil, row("tsk_a", "offline", "low"), t0, "act_a")))
	out, err := ra.Push(context.Background())
	if err != nil || out.Status != PushOffline {
		t.Fatalf("offline push: %+v %v", out, err)
	}
	if p, _ := ra.Pending(); len(p) != 1 || !strings.HasSuffix(p[0].Path, "tsk_a.json") {
		t.Fatalf("pending list: %+v", p)
	}
	write(t, rb, put("tsk_b", NewVersion(nil, row("tsk_b", "online", "low"), t0, "act_b")))
	mustPush(t, rb)
	fault.SetOffline(false)
	mustPush(t, ra)
	for _, id := range []string{"tsk_a", "tsk_b"} {
		if _, ok := readRowAt(t, ra, w.OriginRef(RefLive), id); !ok {
			t.Fatalf("%s lost after reconnect", id)
		}
	}
	if p, _ := ra.Pending(); len(p) != 0 {
		t.Fatalf("a confirmed push clears the pending list: %+v", p)
	}
}

// Side effect 27: a host refusing refs/lore/* keeps the write local.
func TestHostRefusalKeepsWriteLocal(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	fault := gitremote.NewFaultRemote(a.Remote)
	fault.RejectPush(1, gitremote.StatusRemoteRejected)
	ra := newRef(t, a, fault)
	write(t, ra, put("tsk_1", NewVersion(nil, row("tsk_1", "x", "low"), t0, "act_a")))
	out, err := ra.Push(context.Background())
	if err != nil || out.Status != PushRefused {
		t.Fatalf("refused push: %+v %v", out, err)
	}
	if s, _ := ra.Snapshot(context.Background()); !s.Ahead() {
		t.Fatal("the write must stay on the local ref")
	}
}

// §6.6 / side effect 28: a remote live ref that was deleted, or replaced by
// unrelated history without a higher epoch, is never merged: integrate
// refuses with ErrRemoteRewritten and keeps one local backup.
func TestRewrittenRemoteStopsAndBacksUp(t *testing.T) {
	for _, variant := range []string{"deleted", "rewritten"} {
		t.Run(variant, func(t *testing.T) {
			w := gitremotetest.NewWorld(t)
			a, b := w.Clone("a"), w.Clone("b")
			ra, rb := newRef(t, a, a.Remote), newRef(t, b, b.Remote)
			write(t, ra, put("tsk_1", NewVersion(nil, row("tsk_1", "keep", "low"), t0, "act_a")))
			mustPush(t, ra)
			_ = rb.Fetch(context.Background())
			if _, err := rb.Integrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			local, _, _ := b.Repo.ResolveRef(context.Background(), RefLive)
			if variant == "deleted" {
				gitremotetest.Git(t, w.OriginDir, "update-ref", "-d", RefLive)
			} else {
				gitremotetest.Git(t, w.OriginDir, "update-ref", RefLive, "refs/heads/main")
			}
			_ = rb.Fetch(context.Background())
			for i := 0; i < 2; i++ {
				_, err := rb.Integrate(context.Background())
				if !errors.Is(err, ErrRemoteRewritten) {
					t.Fatalf("run %d: want ErrRemoteRewritten, got %v", i, err)
				}
			}
			names, refs, _ := rb.Backups(context.Background())
			if len(names) != 1 || refs[names[0]] != local {
				t.Fatalf("exactly one backup of the local ref, kept across repeats: %v", refs)
			}
			if now, _, _ := b.Repo.ResolveRef(context.Background(), RefLive); now != local {
				t.Fatal("the local ref must not move")
			}
		})
	}
}

// F8 reader path: the remote was compacted (higher epoch, no shared
// history) while this clone held a pending write; only that write is
// re-applied on the compacted commit and nothing of either side is lost.
func TestCompactedRemoteReappliesPendingWrite(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	ra, rb := newRef(t, a, a.Remote), newRef(t, b, b.Remote)
	write(t, rb, put("tsk_old", NewVersion(nil, row("tsk_old", "old", "low"), t0, "act_b")))
	mustPush(t, rb)
	_ = ra.Fetch(context.Background())
	if _, err := ra.Integrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	write(t, ra, put("tsk_pending", NewVersion(nil, row("tsk_pending", "pending on a", "low"), t0, "act_a")))
	// b compacts: one parentless commit, same rows plus a new one, epoch 2.
	write(t, rb, put("tsk_new", NewVersion(nil, row("tsk_new", "b new", "low"), t0, "act_b")))
	mustPush(t, rb)
	tip, _, _ := b.Repo.ResolveRef(context.Background(), RefLive)
	c, err := b.Repo.MakeCommit(context.Background(), gitref.CommitRequest{
		Base: tip, History: gitref.HistoryReplace, Changes: gitref.Changes{Put: map[string][]byte{EpochPath: encodeEpoch(2)}},
		Message: "lore: live compact", Signature: gitremotetest.Sig,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Repo.UpdateRef(context.Background(), RefLive, c.OID, tip); err != nil {
		t.Fatal(err)
	}
	if out, err := rb.ForcePush(context.Background(), tip); err != nil || out.Status != PushDone {
		t.Fatalf("compaction push: %+v %v", out, err)
	}
	_ = ra.Fetch(context.Background())
	res, err := ra.Integrate(context.Background())
	if err != nil || res.Action != IntegrateReapplied || res.Backup == "" {
		t.Fatalf("reader path: %+v %v", res, err)
	}
	mustPush(t, ra)
	for _, id := range []string{"tsk_old", "tsk_new", "tsk_pending"} {
		if _, ok := readRowAt(t, ra, w.OriginRef(RefLive), id); !ok {
			t.Fatalf("%s lost across the compaction", id)
		}
	}
	tr, _ := ra.ReadTree(context.Background(), w.OriginRef(RefLive))
	if tr.Epoch() != 2 {
		t.Fatalf("the epoch only grows: %d", tr.Epoch())
	}
}

// F2 edge case: a binary below _live/meta.json's min_lore_version may not
// write.
func TestWriteBelowFloorRefused(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	meta, _ := Meta{MinLoreVersion: "999.0.0", Features: []string{FeatureLive}}.Encode()
	if _, err := r.Write(context.Background(), WriteRequest{Folder: RootFolder, Raw: map[string][]byte{MetaPath: meta}}); err != nil {
		t.Fatal(err)
	}
	_, err := r.Write(context.Background(), WriteRequest{Folder: RootFolder, BinaryVersion: "0.1.0",
		Rows: put("tsk_1", NewVersion(nil, row("tsk_1", "x", "low"), t0, "a"))})
	if !errors.Is(err, ErrBelowFloor) {
		t.Fatalf("want ErrBelowFloor, got %v", err)
	}
	if err := r.CheckFloor(context.Background(), "0.1.0"); !errors.Is(err, ErrBelowFloor) {
		t.Fatalf("CheckFloor: %v", err)
	}
}

// A reserved folder can never be written.
func TestWriteRefusesReservedFolder(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	if _, err := r.Write(context.Background(), WriteRequest{Folder: "_live", Rows: put("tsk_1", NewVersion(nil, row("tsk_1", "x", "low"), t0, "a"))}); err == nil {
		t.Fatal("_live is reserved")
	}
}

// F7 fencing seam: a staged write moves no ref until CommitStaged, so a
// push that lost leaves nothing behind for a later ordinary push.
func TestStageMovesNothingUntilCommitted(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	ra, rb := newRef(t, a, a.Remote), newRef(t, b, b.Remote)
	write(t, ra, put("tsk_1", NewVersion(nil, row("tsk_1", "x", "low"), t0, "a")))
	mustPush(t, ra)
	before, _, _ := a.Repo.ResolveRef(ctx, RefLive)
	st, err := ra.Stage(ctx, WriteRequest{Folder: RootFolder, BinaryVersion: "0.1.0",
		Rows: put("tsk_1", NewVersion(nil, row("tsk_1", "x", "done"), t0, "a"))})
	if err != nil || st.New == st.Old || st.Old != before {
		t.Fatalf("stage: %+v %v", st, err)
	}
	if now, _, _ := a.Repo.ResolveRef(ctx, RefLive); now != before {
		t.Fatal("Stage must not move the local ref")
	}
	if p, _ := ra.Pending(); len(p) != 0 {
		t.Fatal("Stage records nothing as pending")
	}
	// Lost race: b writes first; the staged push is refused and nothing moved.
	_ = rb.Fetch(ctx)
	_, _ = rb.Integrate(ctx)
	write(t, rb, put("tsk_2", NewVersion(nil, row("tsk_2", "y", "low"), t0, "b")))
	mustPush(t, rb)
	res, err := a.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{st.Update(RefLive)}})
	if err != nil {
		t.Fatal(err)
	}
	if rr, _ := res.Ref(RefLive); rr.Status.Applied() {
		t.Fatal("the staged commit must not fast-forward a moved remote")
	}
	if now, _, _ := a.Repo.ResolveRef(ctx, RefLive); now != before {
		t.Fatal("a lost staged push leaves the local ref alone")
	}
	// Re-run: integrate, stage again, push, commit.
	_ = ra.Fetch(ctx)
	if _, err := ra.Integrate(ctx); err != nil {
		t.Fatal(err)
	}
	st, err = ra.Stage(ctx, WriteRequest{Folder: RootFolder, BinaryVersion: "0.1.0",
		Rows: put("tsk_1", NewVersion(nil, row("tsk_1", "x", "done"), t0, "a"))})
	if err != nil {
		t.Fatal(err)
	}
	res, err = a.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{st.Update(RefLive)}})
	if rr, _ := res.Ref(RefLive); err != nil || !rr.Status.Applied() {
		t.Fatalf("second push: %+v %v", res, err)
	}
	if err := ra.CommitStaged(ctx, st); err != nil {
		t.Fatal(err)
	}
	snap, _ := ra.Snapshot(ctx)
	if snap.Local != st.New || snap.Tracking != st.New || snap.Seen != st.New {
		t.Fatalf("CommitStaged moves local, tracking and seen: %+v", snap)
	}
	if f, ok := readRowAt(t, ra, w.OriginRef(RefLive), "tsk_1"); !ok || f.Body["priority"] != "done" {
		t.Fatal("the staged change is on the remote")
	}
}
