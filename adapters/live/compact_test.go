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
	res, err := ra.Compact(context.Background(), false)
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
	if _, err := r.Compact(context.Background(), false); err != nil {
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
	mustPush(t, r)
	before := w.OriginRef(RefLive)
	res, err := r.Compact(context.Background(), true)
	if err != nil || res.Plan.FromEpoch != 1 || res.Plan.ToEpoch != 2 || res.Plan.Commits != 1 {
		t.Fatalf("plan: %+v %v", res, err)
	}
	if w.OriginRef(RefLive) != before {
		t.Fatal("dry run must not change the ref")
	}
}
