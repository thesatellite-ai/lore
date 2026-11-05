package coord

// expire_test.go — writeSignals' expired-claim staging (expire.go, TIME
// 4.2, later.md 71): a writer that drops somebody's expired task claim
// stages that task's row with the claim span closed at the last heartbeat
// and a claim_log `expire` entry, in the same push; a reclaim, a mission
// claim, an unreadable row and a failed staging add nothing, and the
// writer's own operation never fails because of it.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/model/timeentry"
)

// Valid core actor ids for the expiring holder and the writer (the span is
// attributed only to a core actor id).
const (
	expZombie = "act_0e45506e64357c94a87e70ef1c6bbba1"
	expWriter = "act_0e45506e64357c94a87e70ef1c6bbba2"
)

// expHeartbeat is when the zombie last renews its claim (a task claim).
const expHeartbeat = 5 * time.Minute

// countingRows wraps a Rows: Get answers "no such row" for missing ids,
// Stage fails when a staged batch holds an id in failOn, and every Stage
// call's ids are recorded.
type countingRows struct {
	Rows
	missing map[string]bool
	failOn  map[string]bool
	stages  [][]string
}

func (r *countingRows) Get(ctx context.Context, id string) (Doc, bool, error) {
	if r.missing[id] {
		return nil, false, nil
	}
	return r.Rows.Get(ctx, id)
}

func (r *countingRows) Stage(ctx context.Context, rows ...RowWrite) ([]signals.Companion, error) {
	var got []string
	for _, w := range rows {
		got = append(got, w.ID)
		if r.failOn[w.ID] {
			r.stages = append(r.stages, got)
			return nil, errors.New("stage boom")
		}
	}
	r.stages = append(r.stages, got)
	return r.Rows.Stage(ctx, rows...)
}

// expireEntries are the claim_log `expire` entries of d.
func expireEntries(d Doc) []ClaimLogEntry {
	var out []ClaimLogEntry
	for _, e := range d.ClaimLog() {
		if e.Event == ClaimEventExpire {
			out = append(out, e)
		}
	}
	return out
}

// expireWorld: a zombie claims subject at t0, renews it once at
// t0+expHeartbeat (a task) and dies; the writer then observes the board
// (its own expiry watch starts) and may claim
// tsk_2 before the zombie's lease runs out. The writer's clock is left past
// the zombie's lease and grace.
func expireWorld(t *testing.T, subject string, writerHoldsTsk2 bool) (writer *agent, rows *countingRows) {
	t.Helper()
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask(subject, nil)
	wd.addTask("tsk_2", nil)
	zombie := wd.agent("zombie")
	writer = wd.agent("writer")
	zombie.env.Caller.ActorID, writer.env.Caller.ActorID = expZombie, expWriter
	if _, err := Claim(ctx, zombie.env, subject, ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	zombie.clk.Set(t0.Add(expHeartbeat))
	if !IsMission(subject) {
		if _, err := Renew(ctx, zombie.env, subject); err != nil {
			t.Fatal(err)
		}
	}
	writer.clk.Set(t0.Add(expHeartbeat + time.Minute))
	if _, err := writer.env.Signals.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if writerHoldsTsk2 {
		writer.clk.Set(t0.Add(15 * time.Minute))
		if _, err := Claim(ctx, writer.env, "tsk_2", ClaimOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	rows = &countingRows{Rows: writer.env.Rows, missing: map[string]bool{}, failOn: map[string]bool{}}
	writer.env.Rows = rows
	writer.clk.Set(t0.Add(expHeartbeat + lease.TTL + lease.Grace + 2*time.Minute))
	return writer, rows
}

// TestExpiredClaimIsStagedByAnyWriter: a claim of another task, a renew
// and a release of the writer's own other task each drop the zombie's
// expired claim and write, in their one staging, tsk_1's closed span (at
// the last heartbeat, closed_by_expiry) and one claim_log `expire` entry
// (holder, epoch, at the last heartbeat).
func TestExpiredClaimIsStagedByAnyWriter(t *testing.T) {
	writers := []struct {
		name  string
		holds bool
		write func(ctx context.Context, env Env) error
	}{
		{"claim of another task", false, func(ctx context.Context, env Env) error {
			_, err := Claim(ctx, env, "tsk_2", ClaimOptions{})
			return err
		}},
		{"renew of the writer's own task", true, func(ctx context.Context, env Env) error {
			_, err := Renew(ctx, env, "tsk_2")
			return err
		}},
		{"release of the writer's own task", true, func(ctx context.Context, env Env) error {
			return Release(ctx, env, "tsk_2")
		}},
	}
	for _, c := range writers {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			writer, rows := expireWorld(t, "tsk_1", c.holds)
			if err := c.write(ctx, writer.env); err != nil {
				t.Fatal(err)
			}
			if len(rows.stages) != 1 {
				t.Fatalf("one staging for the write and the expired row, got %v", rows.stages)
			}
			d := writer.row(t, "tsk_1")
			ex := expireEntries(d)
			if len(ex) != 1 || ex[0].ActorID != expZombie || ex[0].Epoch != 1 || !ex[0].At.Equal(t0.Add(expHeartbeat)) {
				t.Fatalf("one expire entry for the zombie at its last heartbeat: %+v", d.ClaimLog())
			}
			es := d.TimeEntries()
			if len(es) != 1 || !es[0].HasFlag(timeentry.FlagClosedByExpiry) || es[0].HasFlag(timeentry.FlagEstimatedEnd) ||
				!es[0].EndTime().Equal(t0.Add(expHeartbeat)) || es[0].Seconds != int64(expHeartbeat/time.Second) {
				t.Fatalf("the zombie's span closes at its last heartbeat: %+v", es)
			}
			b, err := writer.env.Signals.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, held := b.Board[signals.ClaimPath(folder, "tsk_1")]; held {
				t.Fatal("the expired claim is dropped from the board")
			}
		})
	}
}

// TestExpiredClaimStagingAddsNothing: the cases that write no expire
// record, and never fail the writer's own operation.
func TestExpiredClaimStagingAddsNothing(t *testing.T) {
	ctx := context.Background()
	t.Run("a reclaim closes the span itself, no expire entry", func(t *testing.T) {
		writer, rows := expireWorld(t, "tsk_1", false)
		res, err := Claim(ctx, writer.env, "tsk_1", ClaimOptions{})
		if err != nil || !res.Plan.Reclaim {
			t.Fatalf("reclaim: %+v %v", res, err)
		}
		d := writer.row(t, "tsk_1")
		if len(expireEntries(d)) != 0 || len(rows.stages) != 1 {
			t.Fatalf("a reclaim adds no expire entry (stages %v): %+v", rows.stages, d.ClaimLog())
		}
		if es := d.TimeEntries(); len(es) != 1 || !es[0].HasFlag(timeentry.FlagClosedByExpiry) {
			t.Fatalf("the reclaim closes the dead holder's span once: %+v", es)
		}
	})
	t.Run("a write of the same row stages it once, with the expire on the write's version", func(t *testing.T) {
		writer, rows := expireWorld(t, "tsk_1", false)
		if _, err := Update(ctx, writer.env, "tsk_1", func(d Doc, _ time.Time) error {
			d.SetStr(keyTitle, "renamed")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(rows.stages) != 1 || len(rows.stages[0]) != 1 || rows.stages[0][0] != "tsk_1" {
			t.Fatalf("one staging of one tsk_1 row, got %v", rows.stages)
		}
		d := writer.row(t, "tsk_1")
		if d.Str(keyTitle) != "renamed" || len(expireEntries(d)) != 1 {
			t.Fatalf("the update and the expire record share one version: %v", d)
		}
	})
	t.Run("a mission claim is skipped", func(t *testing.T) {
		writer, rows := expireWorld(t, "msn_1", false)
		if _, err := Claim(ctx, writer.env, "tsk_2", ClaimOptions{}); err != nil {
			t.Fatal(err)
		}
		for _, s := range rows.stages {
			for _, id := range s {
				if id == "msn_1" {
					t.Fatalf("a mission row is never staged for an expired claim: %v", rows.stages)
				}
			}
		}
		if ex := expireEntries(writer.row(t, "msn_1")); len(ex) != 0 {
			t.Fatalf("no expire entry on a mission: %+v", ex)
		}
	})
	t.Run("a missing row is skipped, the write goes on", func(t *testing.T) {
		writer, rows := expireWorld(t, "tsk_1", false)
		rows.missing["tsk_1"] = true
		if _, err := Claim(ctx, writer.env, "tsk_2", ClaimOptions{}); err != nil {
			t.Fatalf("an unreadable expired row never fails the write: %v", err)
		}
		delete(rows.missing, "tsk_1")
		if ex := expireEntries(writer.row(t, "tsk_1")); len(ex) != 0 {
			t.Fatalf("nothing written for a row that could not be read: %+v", ex)
		}
		if writer.row(t, "tsk_2").Status() != statusInProgress {
			t.Fatal("the writer's own claim landed")
		}
	})
	t.Run("a failed combined staging falls back to the write's own rows", func(t *testing.T) {
		writer, rows := expireWorld(t, "tsk_1", false)
		rows.failOn["tsk_1"] = true
		if _, err := Claim(ctx, writer.env, "tsk_2", ClaimOptions{}); err != nil {
			t.Fatalf("a failed expired-row staging never fails the write: %v", err)
		}
		if len(rows.stages) != 2 || len(rows.stages[1]) != 1 || rows.stages[1][0] != "tsk_2" {
			t.Fatalf("combined staging, then the claim's row alone: %v", rows.stages)
		}
		if ex := expireEntries(writer.row(t, "tsk_1")); len(ex) != 0 {
			t.Fatalf("the expire record is best effort: %+v", ex)
		}
		if writer.row(t, "tsk_2").Status() != statusInProgress {
			t.Fatal("the writer's own claim landed")
		}
	})
}
