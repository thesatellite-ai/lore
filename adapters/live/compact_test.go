package live

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
)

func TestShouldCompact(t *testing.T) {
	cases := []struct {
		name string
		in   CompactInputs
		want bool
	}{
		{"under threshold", CompactInputs{Commits: 1000, Threshold: 1000, Now: t0}, false},
		{"over threshold, never compacted", CompactInputs{Commits: 1001, Threshold: 1000, Now: t0}, true},
		{"over threshold, compacted an hour ago", CompactInputs{Commits: 5000, Threshold: 1000, LastCompact: t0.Add(-time.Hour), Now: t0}, false},
		{"over threshold, compacted a day ago", CompactInputs{Commits: 5000, Threshold: 1000, LastCompact: t0.Add(-CompactMinInterval), Now: t0}, true},
		{"threshold 0 turns it off", CompactInputs{Commits: 5000, Threshold: 0, Now: t0}, false},
	}
	for _, c := range cases {
		if got := ShouldCompact(c.in); got != c.want {
			t.Fatalf("%s: %v", c.name, got)
		}
	}
}

// F8: one parentless commit, same rows, epoch +1, local backup kept and not
// pushed; a writer racing the lease push loses nothing.
func TestCompactKeepsRowsRaisesEpochAndSurvivesARacingWriter(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	fault := gitremote.NewFaultRemote(a.Remote)
	ra, rb := newRef(t, a, fault), newRef(t, b, b.Remote)
	for _, id := range []string{"tsk_1", "tsk_2"} {
		write(t, ra, put(id, NewVersion(nil, row(id, id, "low"), t0, "act_a")))
		mustPush(t, ra)
	}
	_ = rb.Fetch(context.Background())
	if _, err := rb.Integrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	// b writes and pushes right before a's compaction lease push.
	pushes := fault.Pushes()
	fault.BeforePush(pushes+1, func(ctx context.Context) error {
		write(t, rb, put("tsk_racer", NewVersion(nil, row("tsk_racer", "racer", "low"), t0, "act_b")))
		_, err := rb.Push(ctx)
		return err
	})
	res, err := ra.Compact(context.Background(), CompactRequest{PushWaiting: true})
	if err != nil || res.Status != PushDone || res.Backup == "" {
		t.Fatalf("compact: %+v %v", res, err)
	}
	if n := commitCount(t, w.OriginDir); n != 1 {
		t.Fatalf("compacted ref has %d commits", n)
	}
	// The racer's push made the first lease push fail; the compaction
	// started again (one backup per attempt) instead of overwriting it.
	if names, _, _ := ra.Backups(context.Background()); len(names) < 2 {
		t.Fatalf("the lost lease must have restarted the compaction: backups %v", names)
	}
	tip := w.OriginRef(RefLive)
	for _, id := range []string{"tsk_1", "tsk_2", "tsk_racer"} {
		if _, ok := readRowAt(t, ra, tip, id); !ok {
			t.Fatalf("%s lost in the compaction race", id)
		}
	}
	tr, _ := ra.ReadTree(context.Background(), tip)
	if tr.Epoch() != FirstEpoch+1 || tr.CompactedAt().IsZero() {
		t.Fatalf("epoch %d, compacted_at %v", tr.Epoch(), tr.CompactedAt())
	}
	if refs, _ := w.Origin.ListRefs(context.Background(), "refs/lore-backup/"); len(refs) != 0 {
		t.Fatal("backups are never pushed")
	}
}

// §6.6: a purged id leaves the tree at once and the compacted history.
func TestPurgeThenCompactDropsTheRowFromHistory(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	write(t, r, put("tsk_leak", NewVersion(nil, row("tsk_leak", "secret-ish", "low"), t0, "act_a")))
	write(t, r, put("tsk_ok", NewVersion(nil, row("tsk_ok", "fine", "low"), t0, "act_a")))
	mustPush(t, r)
	if _, err := r.Purge(context.Background(), []string{"tsk_leak"}, "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if _, ok := readRowAt(t, r, RefLive, "tsk_leak"); ok {
		t.Fatal("purge removes the row from the tree")
	}
	if _, err := r.Compact(context.Background(), CompactRequest{PushWaiting: true}); err != nil {
		t.Fatal(err)
	}
	for _, rev := range []string{w.OriginRef(RefLive)} {
		if _, ok := readRowAt(t, r, rev, "tsk_leak"); ok {
			t.Fatal("compaction must not keep the purged row")
		}
	}
	if n := commitCount(t, w.OriginDir); n != 1 {
		t.Fatalf("history rewritten to one commit, got %d", n)
	}
	tr, _ := r.ReadTree(context.Background(), w.OriginRef(RefLive))
	if p, _ := tr.Purged(); !p["tsk_leak"] {
		t.Fatal("the purged id stays listed so stale clones drop it")
	}
	if _, ok := readRowAt(t, r, w.OriginRef(RefLive), "tsk_ok"); !ok {
		t.Fatal("other rows survive")
	}
}

func TestCompactDryRunChangesNothing(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	write(t, r, put("tsk_1", NewVersion(nil, row("tsk_1", "x", "low"), t0, "a")))
	write(t, r, put("tsk_2", NewVersion(nil, row("tsk_2", "y", "low"), t0, "a")))
	mustPush(t, r)
	before := w.OriginRef(RefLive)
	res, err := r.Compact(context.Background(), CompactRequest{DryRun: true, PushWaiting: true})
	if err != nil || res.Plan.FromEpoch != 1 || res.Plan.ToEpoch != 2 || res.Plan.Commits != 2 {
		t.Fatalf("plan: %+v %v", res, err)
	}
	if w.OriginRef(RefLive) != before {
		t.Fatal("dry run must not change the ref")
	}
}

func TestCompactNeeded(t *testing.T) {
	cases := []struct {
		commits, purged int
		want            bool
	}{
		{0, 0, false}, {1, 0, false}, {1, 1, true}, {2, 0, true}, {1000, 3, true},
	}
	for _, c := range cases {
		if got := CompactNeeded(c.commits, c.purged); got != c.want {
			t.Errorf("CompactNeeded(%d commits, %d purged files) = %v, want %v", c.commits, c.purged, got, c.want)
		}
	}
}

// F8 epoch rule: each compaction that rewrites history raises the epoch by
// exactly one; compacting a ref that is already one commit with nothing
// purged writes nothing and keeps the epoch, with and without --dry-run;
// the plan's epochs match the tree's epoch after the run.
func TestCompactEpochRaisesByOneOnlyWhenHistoryIsRewritten(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	epochAt := func() int {
		tr, err := r.ReadTree(ctx, w.OriginRef(RefLive))
		if err != nil {
			t.Fatal(err)
		}
		return tr.Epoch()
	}
	for _, id := range []string{"tsk_1", "tsk_2", "tsk_3"} {
		write(t, r, put(id, NewVersion(nil, row(id, id, "low"), t0, "a")))
	}
	mustPush(t, r)
	steps := []struct {
		name       string
		dry        bool
		from, to   int
		status     PushStatus
		wantEpoch  int
		wantCommit bool
	}{
		{"three commits", false, 1, 2, PushDone, 2, true},
		{"again, already one commit", false, 2, 2, PushNothing, 2, false},
		{"dry run, already one commit", true, 2, 2, PushNothing, 2, false},
	}
	for _, s := range steps {
		before := w.OriginRef(RefLive)
		res, err := r.Compact(ctx, CompactRequest{DryRun: s.dry, PushWaiting: true})
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if res.Status != s.status || res.Plan.FromEpoch != s.from || res.Plan.ToEpoch != s.to {
			t.Fatalf("%s: status %s, epoch %d → %d; want %s, %d → %d", s.name, res.Status, res.Plan.FromEpoch, res.Plan.ToEpoch, s.status, s.from, s.to)
		}
		if got := epochAt(); got != s.wantEpoch {
			t.Fatalf("%s: remote epoch %d, want %d", s.name, got, s.wantEpoch)
		}
		if changed := w.OriginRef(RefLive) != before; changed != s.wantCommit {
			t.Fatalf("%s: remote ref changed = %v, want %v", s.name, changed, s.wantCommit)
		}
	}
	// A new write after the compaction keeps the epoch (only compaction
	// raises it), and the next compaction raises it by exactly one.
	write(t, r, put("tsk_4", NewVersion(nil, row("tsk_4", "four", "low"), t0, "a")))
	mustPush(t, r)
	if got := epochAt(); got != 2 {
		t.Fatalf("a write changed the epoch: %d", got)
	}
	if res, err := r.Compact(ctx, CompactRequest{PushWaiting: true}); err != nil || res.Plan.ToEpoch != 3 || epochAt() != 3 {
		t.Fatalf("second real compaction: %+v %v, remote epoch %d", res, err, epochAt())
	}
}

// S-1: a compaction with nothing to compact pushes nothing, not even a
// local write that waits for lore sync (live.autopush false): the remote
// has no live ref before and after. Old code pushed the waiting write
// before it looked at the plan.
func TestCompactWithNothingToDoPushesNoWaitingWrite(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	write(t, r, put("tsk_1", NewVersion(nil, row("tsk_1", "x", "low"), t0, "a")))
	for _, dry := range []bool{false, true} {
		res, err := r.Compact(ctx, CompactRequest{DryRun: dry})
		if err != nil || res.Status != PushNothing {
			t.Fatalf("dry %v: %+v %v", dry, res, err)
		}
		if got := w.OriginRef(RefLive); got != "" {
			t.Fatalf("dry %v: compact pushed the waiting write: origin %s = %s", dry, RefLive, got)
		}
	}
}

// S-1 (devco D-881): a compaction asked without PushWaiting (live.autopush
// off) never pushes the writes that wait for lore sync. With no live ref on
// the remote it compacts on this machine only (KeptLocal) and lore sync
// then sends one clean commit; with a remote live ref it answers
// PushWaiting and changes nothing, dry run or not. With nothing waiting it
// still rewrites and force-pushes its own rewrite (the question named it).
// Old code pushed the waiting writes, then force-pushed the rewrite.
func TestCompactNeverPushesWaitingWritesWithoutPushWaiting(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	write(t, r, put("tsk_1", NewVersion(nil, row("tsk_1", "x", "low"), t0, "a")))
	write(t, r, put("tsk_2", NewVersion(nil, row("tsk_2", "y", "low"), t0, "a")))

	// no live ref on the remote: the plan and the compaction stay here
	res, err := r.Compact(ctx, CompactRequest{DryRun: true})
	if err != nil || !res.KeptLocal || res.Status != "" || res.Plan.Commits < 2 || w.OriginRef(RefLive) != "" {
		t.Fatalf("dry run, no remote ref: %+v %v (origin %q)", res, err, w.OriginRef(RefLive))
	}
	res, err = r.Compact(ctx, CompactRequest{})
	if err != nil || !res.KeptLocal || res.Status != PushKeptLocal || res.Backup == "" {
		t.Fatalf("no remote ref: %+v %v", res, err)
	}
	if got := w.OriginRef(RefLive); got != "" {
		t.Fatalf("compact pushed the waiting writes: origin %s = %s", RefLive, got)
	}
	if n, _ := r.CommitCount(ctx); n != 1 {
		t.Fatalf("local ref has %d commits after compacting", n)
	}
	// lore sync sends the compacted ref: one commit, every row, epoch 2.
	mustPush(t, r)
	if n := commitCount(t, w.OriginDir); n != 1 {
		t.Fatalf("origin has %d commits after sync", n)
	}
	tip := w.OriginRef(RefLive)
	for _, id := range []string{"tsk_1", "tsk_2"} {
		if _, ok := readRowAt(t, r, tip, id); !ok {
			t.Fatalf("%s lost by the local compaction", id)
		}
	}

	// a remote live ref and a write waiting on top of it: refused, nothing changes
	write(t, r, put("tsk_3", NewVersion(nil, row("tsk_3", "z", "low"), t0, "a")))
	before, _ := r.Snapshot(ctx)
	for _, dry := range []bool{true, false} {
		res, err := r.Compact(ctx, CompactRequest{DryRun: dry})
		if err != nil || res.Status != PushWaiting || res.KeptLocal || res.Backup != "" {
			t.Fatalf("write waiting, dry %v: %+v %v", dry, res, err)
		}
		if got := w.OriginRef(RefLive); got != tip {
			t.Fatalf("write waiting, dry %v: compact pushed: origin %s → %s", dry, tip, got)
		}
		if after, _ := r.Snapshot(ctx); after.Local != before.Local {
			t.Fatalf("write waiting, dry %v: the local ref changed %s → %s", dry, before.Local, after.Local)
		}
	}

	// nothing waits: the rewrite itself is what was asked, it goes out
	mustPush(t, r)
	res, err = r.Compact(ctx, CompactRequest{})
	if err != nil || res.Status != PushDone || res.KeptLocal || commitCount(t, w.OriginDir) != 1 {
		t.Fatalf("nothing waiting: %+v %v (origin commits %d)", res, err, commitCount(t, w.OriginDir))
	}
}
