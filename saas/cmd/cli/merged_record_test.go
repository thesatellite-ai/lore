package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"dbent/gen/ent"
	"saas/pkg/aicoder/mergewatch"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/schema"
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
// rows records the stamp in its branch data, through the core (tasks are
// core-owned in 0.1.18). Two clones holding the same done row that find the
// same merge at different wall-clock times must end with the same
// merged_at, the merge commit's date, and their two copies must merge on
// the next pull without a conflict (trial finding F5). In v0.1.17 that
// needed the same bytes (updated_at was the merge time); the core orders
// copies by _hlc, so the check is the core's own merge of the two files.
func TestRecordMergedBranch_TwoClonesWriteTheSameRow(t *testing.T) {
	ctx := context.Background()
	a := newCoreFixture(t)
	id := idPattern.FindString(a.lore("task", "add", "fix totals", "--commitment=accepted").stdout)
	if id == "" {
		t.Fatal("no task id")
	}
	a.lore("task", "start", id)
	a.lore("task", "done", id)
	a.git("add", "-A")
	a.git("commit", "-q", "-m", "done")
	base := a.taskRow(id)
	// The second clone: the same commit in another checkout, another HOME.
	b := a.copyTo(t)
	f := mergewatch.Finding{Method: mergewatch.MethodAncestry, Commit: "f0fd74c", At: time.Date(2026, 11, 3, 10, 15, 0, 0, time.UTC)}
	stamp := func(cf *coreFixture) []byte {
		t.Helper()
		cf.mount()
		recordMergedBranch(ctx, nil, []foundMerge{{task: &ent.Task{ID: id}, finding: f}})
		d, ok, err := coreDoc(ctx, tableTasks, id)
		if err != nil || !ok {
			t.Fatalf("read %s: %v %v", id, ok, err)
		}
		if got := d.Time(keyMergedAtDoc); !got.Equal(f.At) {
			t.Fatalf("merged_at must be the merge commit's date %v: %v", f.At, got)
		}
		return cf.taskRow(id)
	}
	ra := stamp(a)
	time.Sleep(2 * time.Millisecond) // the two clones run at different instants
	rb := stamp(b)
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	merged, conflict, err := merge.Driver{Registry: reg}.Merge(base, ra, rb)
	if err != nil || conflict {
		t.Fatalf("the two stamped copies must merge cleanly: conflict=%v err=%v\n%s", conflict, err, merged)
	}
	if !strings.Contains(string(merged), `"merged_at"`) || !strings.Contains(string(merged), f.At.Format("2006-01-02T15:04:05")) {
		t.Fatalf("merged copy lost merged_at: %s", merged)
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
