package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMoveTimeIntoClient pins TQ3: a task moved into a client repo keeps
// its time entries and other internal fields back (one line each); with
// --with-time totals it carries time_totals (per worker label, overlap of
// one worker once, manual in full, date range), never spans or actor ids.
func TestMoveTimeIntoClient(t *testing.T) {
	ctx := context.Background()
	w := newProvWorld(t, fakeProv{})
	w.project(t, store.SourceRepo, "app")
	prj := w.project(t, agency, "acme-hub")
	put := func(r row.Row) {
		t.Helper()
		sr, err := store.FromRow(r)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.data[agency].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
			t.Fatal(err)
		}
	}
	h := hlc.Next(hlc.HLC{}, w.eng.wall(), testActor)
	put(row.Row{Table: schema.TableActors, ID: testActor.Any(), HLC: h, Fields: map[string]value.Value{
		"display_name": txt("mira"), "kind": txt(schema.ActorKindHuman)}})
	ref, err := value.RefOf(prj)
	if err != nil {
		t.Fatal(err)
	}
	a := testActor.String()
	entries, err := value.JSONOf([]byte(`[` +
		`{"id":"tme_0192f0a0000070008000000000000001","kind":"person","source":"status","actor_id":"` + a + `","start":"2026-11-17T09:00:00.000Z","end":"2026-11-17T11:00:00.000Z","day":"2026-11-17","seconds":7200,"created_at":"2026-11-17T11:00:00.000Z","updated_at":"2026-11-17T11:00:00.000Z"},` +
		`{"id":"tme_0192f0a0000070008000000000000002","kind":"person","source":"status","actor_id":"` + a + `","start":"2026-11-17T10:00:00.000Z","end":"2026-11-17T12:00:00.000Z","day":"2026-11-17","seconds":7200,"created_at":"2026-11-17T12:00:00.000Z","updated_at":"2026-11-17T12:00:00.000Z"},` +
		`{"id":"tme_0192f0a0000070008000000000000003","kind":"manual","source":"manual","actor_id":"` + a + `","day":"2026-11-18","seconds":1800,"created_at":"2026-11-18T12:00:00.000Z","updated_at":"2026-11-18T12:00:00.000Z"}]`))
	if err != nil {
		t.Fatal(err)
	}
	task := func() row.Row {
		id, nerr := w.eng.ids.NewID(mustPrefix(t, w, schema.TableTasks))
		if nerr != nil {
			t.Fatal(nerr)
		}
		r := row.Row{Table: schema.TableTasks, ID: id, HLC: h, Fields: map[string]value.Value{
			"project_id": ref, "title": txt("Rate limit"), "status": txt("todo"), "time_entries": entries, "claim_epoch": value.Int(3)}}
		put(r)
		return r
	}

	plain := task()
	got, err := w.eng.Move(ctx, Ref{ID: plain.ID}, store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"time_entries", "claim_epoch", "time_totals"} {
		if _, ok := got.Row.Fields[f]; ok {
			t.Errorf("plain move: %s reached the client repo", f)
		}
	}
	if len(got.Warnings) != 2 || got.Warnings[0].Code != WarnTimeKeptBack || got.Warnings[0].Message != "kept back: time (3 entries)" || got.Warnings[1].Code != WarnInternalKeptBack {
		t.Fatalf("plain move warnings: %+v", got.Warnings)
	}

	withTotals := task()
	got, err = w.eng.MoveWith(ctx, Ref{ID: withTotals.ID}, store.SourceRepo, MoveOptions{WithTime: TimeTotals})
	if err != nil {
		t.Fatal(err)
	}
	tt, ok := got.Row.Fields["time_totals"]
	if !ok {
		t.Fatal("--with-time totals: no time_totals")
	}
	raw, err := tt.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `"effort_seconds":12600`
	if s := string(raw); !strings.Contains(s, want) || !strings.Contains(s, `"label":"mira"`) || !strings.Contains(s, `"from":"2026-11-17"`) ||
		!strings.Contains(s, `"to":"2026-11-18"`) || strings.Contains(s, a) || strings.Contains(s, "tme_") {
		t.Fatalf("time_totals = %s", s)
	}
	if last := got.Warnings[len(got.Warnings)-1]; last.Code != WarnTimeTotals || !strings.Contains(last.Message, "mira 3h 30m") {
		t.Fatalf("totals line: %+v", last)
	}
}
