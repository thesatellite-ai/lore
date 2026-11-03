package main

import (
	"context"
	"testing"
	"time"

	"dbent/gen/ent"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/mergewatch"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
)

// TestMergedUpdatedAt pins the branch-row stamp's updated_at: the later of
// the row's own and the merge time, never the wall clock, so it is a pure
// function of the row and the finding.
func TestMergedUpdatedAt(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		old, merged time.Time
		want        time.Time
	}{
		{"merged after the last edit", base, base.Add(time.Hour), base.Add(time.Hour)},
		{"merged before done was marked", base.Add(time.Hour), base, base.Add(time.Hour)},
		{"same instant", base, base, base},
		{"non-UTC input is normalised", base.In(time.FixedZone("IST", 19800)), base.Add(time.Minute).In(time.FixedZone("PST", -28800)), base.Add(time.Minute)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergedUpdatedAt(c.old, c.merged)
			if !got.Equal(c.want) || got.Location() != time.UTC {
				t.Fatalf("mergedUpdatedAt(%v, %v) = %v, want %v UTC", c.old, c.merged, got, c.want)
			}
		})
	}
}

// TestRecordMergedBranch_TwoClonesWriteTheSameRow: a checkout without live
// rows records the stamp in its branch data. Two clones holding the same
// done row that find the same merge at different wall-clock times must end
// with the same merged_at and updated_at (the same bytes), or their copies
// would conflict on the next pull (trial finding F5).
func TestRecordMergedBranch_TwoClonesWriteTheSameRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	doneAt := time.Date(2026, 11, 3, 10, 10, 0, 0, time.UTC)
	f := mergewatch.Finding{Method: mergewatch.MethodAncestry, Commit: "f0fd74c", At: doneAt.Add(5 * time.Minute)}
	stamp := func() *ent.Task {
		c := openTestClient(t)
		p, err := c.Project.Create().SetName("app").Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		tk, err := c.Task.Create().SetID("tsk_01a1a0e282f774e09e3b28074ca19dd3").SetProjectID(p.ID).SetTitle("fix totals").
			SetStatus(entTask.StatusDone).SetUpdatedAt(doneAt).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // the two clones run at different instants
		recordMergedBranch(ctx, c, []foundMerge{{task: tk, finding: f}})
		got, err := c.Task.Get(ctx, tk.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	a, b := stamp(), stamp()
	if a.MergedAt == nil || b.MergedAt == nil || !a.MergedAt.Equal(f.At) || !b.MergedAt.Equal(f.At) {
		t.Fatalf("merged_at must be the merge commit's date %v: %v / %v", f.At, a.MergedAt, b.MergedAt)
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		t.Fatalf("two clones stamping the same merge must write the same updated_at: %v vs %v", a.UpdatedAt, b.UpdatedAt)
	}
	if !a.UpdatedAt.Equal(f.At) {
		t.Fatalf("updated_at = %v, want the merge time %v", a.UpdatedAt, f.At)
	}
}

// seqRows returns one row version per Get (the last one repeats) and
// counts stagings.
type seqRows struct {
	docs   []coord.Doc
	gets   int
	staged []coord.Doc
}

func (r *seqRows) Get(context.Context, string) (coord.Doc, bool, error) {
	d := r.docs[min(r.gets, len(r.docs)-1)]
	r.gets++
	return d.Clone(), d != nil, nil
}

func (r *seqRows) Stage(_ context.Context, rows ...coord.RowWrite) ([]signals.Companion, error) {
	for _, w := range rows {
		r.staged = append(r.staged, w.Doc)
	}
	return nil, nil
}

// racingSignals runs the decision `rounds` times, as signals.Store does
// when the push loses a race and it decides again on fresh state.
type racingSignals struct{ rounds int }

func (s racingSignals) Write(_ context.Context, decide signals.Decide) (signals.Result, error) {
	for i := 0; i < s.rounds; i++ {
		if _, err := decide(signals.View{}); err != nil {
			return signals.Result{}, err
		}
	}
	return signals.Result{}, nil
}

// TestStampMerged_DecidesOnTheFreshRow: the stamp is decided inside the
// signals write, on a fresh read. A row someone else already stamped, or
// stamped while we lost a race, is not written again and is not reported
// as recorded by us; only merged_at changes (updated_at is kept, so two
// finders stage the same bytes).
func TestStampMerged_DecidesOnTheFreshRow(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 3, 10, 41, 0, 0, time.UTC)
	open := coord.Doc{"id": "tsk_1", "status": "done", "updated_at": "2026-11-03T10:30:00.000000000Z"}
	stamped := open.Clone()
	stamped.SetTime("merged_at", at)
	cases := []struct {
		name         string
		docs         []coord.Doc
		rounds       int
		wantRecorded bool
	}{
		{"first finder stamps", []coord.Doc{open}, 1, true},
		{"already stamped: nothing to do", []coord.Doc{stamped}, 1, false},
		{"lost the race to another finder", []coord.Doc{open, stamped}, 2, false},
		{"lost an unrelated race, still unstamped", []coord.Doc{open, open}, 2, true},
		{"reopened task", []coord.Doc{{"id": "tsk_1", "status": "in_progress"}}, 1, false},
		{"row gone", []coord.Doc{nil}, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := &seqRows{docs: c.docs}
			ok, err := stampMerged(context.Background(), rows, racingSignals{rounds: c.rounds}, "tsk_1", at)
			if err != nil || ok != c.wantRecorded {
				t.Fatalf("recorded=%v err=%v, want recorded=%v", ok, err, c.wantRecorded)
			}
			if !c.wantRecorded {
				return
			}
			last := rows.staged[len(rows.staged)-1]
			if !last.Time("merged_at").Equal(at) || last.Str("updated_at") != open.Str("updated_at") {
				t.Fatalf("staged %v: want merged_at %v and updated_at unchanged", last, at)
			}
		})
	}
}
