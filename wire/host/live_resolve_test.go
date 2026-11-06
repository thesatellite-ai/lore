package host

// live_resolve_test.go — G-SYNC-1: `lore sync conflicts` / `lore sync
// resolve <n> --take kept|other` over the live rows of the cwd project
// (liveSync), and `lore sync status` where live rows do not apply.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/model/errs"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// conflictBlock is a merge3-shaped conflict between two titles.
func conflictBlock(ours, theirs string) string {
	return "<<<<<<< ours\n" + ours + "\n=======\n" + theirs + "\n>>>>>>> theirs"
}

// markLive rewrites the live file of table/id with title (a conflict
// block or plain text) as a new live version, the way a live merge
// leaves a text clash (F4).
func (f ltFixture) markLive(t *testing.T, table, id, title string) {
	t.Helper()
	_, files, err := f.b.liveFiles(f.ctx, f.s)
	if err != nil {
		t.Fatal(err)
	}
	k := live.RowKey{Table: table, ID: id}
	prev, ok := files[k]
	if !ok {
		t.Fatalf("fixture: %s is not live", k)
	}
	body := prev.Body.Clone()
	body["title"] = canonjson.String(title)
	next := live.NewVersion(&prev, body, f.s.clk.Now(), liveAnonymousWriter)
	if _, err = f.s.ref.Write(f.ctx, live.WriteRequest{Folder: live.RootFolder, Rows: map[live.RowKey]live.File{k: next}, BinaryVersion: "0.1.18"}); err != nil {
		t.Fatal(err)
	}
}

// liveTitle is the live title of table/id.
func (f ltFixture) liveTitle(t *testing.T, table, id string) string {
	t.Helper()
	_, files, err := f.b.liveFiles(f.ctx, f.s)
	if err != nil {
		t.Fatal(err)
	}
	return files[live.RowKey{Table: table, ID: id}].Body.Str("title")
}

// twoConflicts is a live fixture with two conflicted tasks (A, B, in
// table/id order) and one clean memory.
func twoConflicts(t *testing.T) ltFixture {
	t.Helper()
	f := newLiveFixture(t)
	if err := f.b.Write(f.ctx, []store.Change{
		{Row: ltRow(t, schema.TableTasks, ltTaskA, 1, "a", false)},
		{Row: ltRow(t, schema.TableTasks, ltTaskB, 1, "b", false)},
	}); err != nil {
		t.Fatal(err)
	}
	f.markLive(t, schema.TableTasks, ltTaskA, conflictBlock("A mine", "A theirs"))
	f.markLive(t, schema.TableTasks, ltTaskB, "before\n"+conflictBlock("B mine", "B theirs")+"\nafter")
	return f
}

// TestLiveSyncConflictsAndResolve: numbering in table/id order, both
// --take sides, settling only the named row, the list shrinking, and the
// refusals (bad --take, bad numbers, an already settled number).
func TestLiveSyncConflictsAndResolve(t *testing.T) {
	cases := []struct {
		name         string
		n, take      string
		wantA, wantB string
		wantLeft     int
	}{
		{name: "kept #1", n: "1", take: "kept", wantA: "A mine", wantB: "before\n" + conflictBlock("B mine", "B theirs") + "\nafter", wantLeft: 1},
		{name: "other #1", n: "1", take: "other", wantA: "A theirs", wantB: "before\n" + conflictBlock("B mine", "B theirs") + "\nafter", wantLeft: 1},
		{name: "other #2 keeps surrounding lines", n: "2", take: "other", wantA: conflictBlock("A mine", "A theirs"), wantB: "before\nB theirs\nafter", wantLeft: 1},
		{name: "#n spelling", n: "#2", take: "kept", wantA: conflictBlock("A mine", "A theirs"), wantB: "before\nB mine\nafter", wantLeft: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := twoConflicts(t)
			l := liveSync{h: f.s.h}
			list, err := l.SyncConflicts(f.ctx, opssync.ConflictsIn{})
			if err != nil || list.Count != 2 || list.Data[0].RowID != ltTaskA || list.Data[1].RowID != ltTaskB {
				t.Fatalf("conflicts before: %+v %v", list.Data, err)
			}
			if !strings.Contains(list.Lines[0], "lore sync resolve 1 --take kept|other") {
				t.Fatalf("the conflict line names the resolve command: %q", list.Lines[0])
			}
			wrote := f.s.wrote
			out, err := l.SyncResolve(f.ctx, opssync.ResolveIn{ID: c.n, Take: c.take})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.Done, "took "+c.take) {
				t.Fatalf("done line: %q", out.Done)
			}
			if got := f.liveTitle(t, schema.TableTasks, ltTaskA); got != c.wantA {
				t.Fatalf("A title %q, want %q", got, c.wantA)
			}
			if got := f.liveTitle(t, schema.TableTasks, ltTaskB); got != c.wantB {
				t.Fatalf("B title %q, want %q", got, c.wantB)
			}
			after, err := l.SyncConflicts(f.ctx, opssync.ConflictsIn{})
			if err != nil || after.Count != c.wantLeft {
				t.Fatalf("conflicts after: %d %v", after.Count, err)
			}
			if f.s.wrote <= wrote {
				t.Fatal("the settled row counts as written, so the run's finish pushes it")
			}
		})
	}
}

// TestLiveSyncResolveRefusals: every refusal writes nothing.
func TestLiveSyncResolveRefusals(t *testing.T) {
	cases := []struct {
		name, n, take string
		code          errs.Code
	}{
		{"take empty", "1", "", errs.InvalidInput},
		{"take unknown", "1", "mine", errs.InvalidInput},
		{"take upper case", "1", "KEPT", errs.InvalidInput},
		{"number zero", "0", "kept", errs.InvalidInput},
		{"number negative", "-1", "kept", errs.InvalidInput},
		{"number text", "one", "kept", errs.InvalidInput},
		{"number empty", "", "kept", errs.InvalidInput},
		{"past the list", "3", "kept", errs.NotFound},
		{"huge", "999999999", "other", errs.NotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := twoConflicts(t)
			before, wrote := f.liveTitle(t, schema.TableTasks, ltTaskA), f.s.wrote
			_, err := liveSync{h: f.s.h}.SyncResolve(f.ctx, opssync.ResolveIn{ID: c.n, Take: c.take})
			if !errs.Has(err, c.code) {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if got := f.liveTitle(t, schema.TableTasks, ltTaskA); got != before || f.s.wrote != wrote {
				t.Fatalf("a refusal wrote: title %q wrote %d -> %d", got, wrote, f.s.wrote)
			}
		})
	}
}

// TestLiveSyncResolveRefusesLeftoverMarkers: a nested block (a block inside
// a side) leaves markers after one pass: refused, nothing written.
func TestLiveSyncResolveRefusesLeftoverMarkers(t *testing.T) {
	f := newLiveFixture(t)
	if err := f.b.Write(f.ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 1, "a", false)}}); err != nil {
		t.Fatal(err)
	}
	nested := "<<<<<<< ours\n" + conflictBlock("x", "y") + "\n=======\nz\n>>>>>>> theirs"
	f.markLive(t, schema.TableTasks, ltTaskA, nested)
	_, err := liveSync{h: f.s.h}.SyncResolve(f.ctx, opssync.ResolveIn{ID: "1", Take: "other"})
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "still holds conflict markers") {
		t.Fatalf("err = %v", err)
	}
	if got := f.liveTitle(t, schema.TableTasks, ltTaskA); got != nested {
		t.Fatalf("a refused resolve changed the row: %q", got)
	}
}

// TestLiveSyncResolveLeakCheckRefuses: the F16 live-write check runs on
// the settled row; its refusal writes nothing (break-verified: without the
// check in SyncResolve this test fails).
func TestLiveSyncResolveLeakCheckRefuses(t *testing.T) {
	f := twoConflicts(t)
	blocked := errs.New(errs.LeakBlocked, "test leak check")
	f.s.h.cfg.LeakCheck = func(context.Context, string, map[string][]byte) error { return blocked }
	before := f.liveTitle(t, schema.TableTasks, ltTaskA)
	_, err := liveSync{h: f.s.h}.SyncResolve(f.ctx, opssync.ResolveIn{ID: "1", Take: "kept"})
	if !errors.Is(err, blocked) {
		t.Fatalf("err = %v, want the leak refusal", err)
	}
	if got := f.liveTitle(t, schema.TableTasks, ltTaskA); got != before {
		t.Fatalf("a leak-refused resolve wrote: %q", got)
	}
}

// TestLiveSyncConflictsNone: no conflicted row: one "· no conflicts" line,
// an empty (not null) data list, resolve 1 is E_NOT_FOUND.
func TestLiveSyncConflictsNone(t *testing.T) {
	f := newLiveFixture(t)
	if err := f.b.Write(f.ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 1, "clean", false)}}); err != nil {
		t.Fatal(err)
	}
	l := liveSync{h: f.s.h}
	out, err := l.SyncConflicts(f.ctx, opssync.ConflictsIn{})
	if err != nil || out.Count != 0 || out.Data == nil || len(out.Lines) != 1 || out.Lines[0] != "· no conflicts" {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err = l.SyncResolve(f.ctx, opssync.ResolveIn{ID: "1", Take: "kept"}); !errs.Has(err, errs.NotFound) {
		t.Fatalf("err = %v", err)
	}
}

// TestLiveSyncStatusLiveOff: with LORE_LIVE=0 sync status answers (exit 0)
// with the live-off line instead of E_UNSUPPORTED; conflicts and resolve
// keep their E_UNSUPPORTED (the core lists marked files around them).
func TestLiveSyncStatusLiveOff(t *testing.T) {
	f := newLiveFixture(t)
	t.Setenv(EnvLive, envOff)
	l := liveSync{h: f.s.h}
	out, err := l.SyncStatus(f.ctx, opssync.StatusIn{})
	if err != nil {
		t.Fatalf("status with live off: %v", err)
	}
	if len(out.Lines) == 0 || out.Lines[len(out.Lines)-1] != statusLiveOff || out.Kind != opssync.KindStatus {
		t.Fatalf("status lines %q kind %q", out.Lines, out.Kind)
	}
	if _, err = l.SyncConflicts(f.ctx, opssync.ConflictsIn{}); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("conflicts with live off: %v", err)
	}
}
