package opclock_test

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// at is the fixed wall clock: two days before any real run of this test,
// so a stamp read from the system clock can never pass for it.
var at = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

// TestOpsStampOnTheEngineClock: every op that stamps a time of its own
// (the docs examples ran under LORE_NOW and these came out on the real
// clock) writes the engine's wall clock, the same instant as the row's
// HLC. One case per stamping op family: content (snapshot taken_at,
// pattern archive), knowledge (rule archive), memory (tx_at, archive,
// invalidate), work (task start/done, mission done, reminder done).
func TestOpsStampOnTheEngineClock(t *testing.T) {
	type tc struct {
		name   string
		add    string // op that creates the row
		in     string
		then   string // op run on the new id ("" = none)
		thenIn string // extra JSON fields for then (besides id)
		field  string
	}
	cases := []tc{
		{"snapshot taken_at", "snapshot.add", `{"title":"S","body":"b"}`, "", "", "taken_at"},
		{"pattern archive", "pattern.add", `{"title":"P","body":"b"}`, "pattern.archive", "", "archived_at"},
		{"rule archive", "rule.add", `{"body":"Run tests."}`, "rule.archive", "", "archived_at"},
		{"memory tx_at", "memory.add", `{"body":"one fact"}`, "", "", "tx_at"},
		{"memory archive", "memory.add", `{"body":"two fact"}`, "memory.archive", "", "archived_at"},
		{"memory invalidate", "memory.add", `{"body":"three fact"}`, "memory.invalidate", "", "valid_until"},
		{"task start", "task.add", `{"title":"T"}`, "task.start", "", "started_at"},
		{"task done", "task.add", `{"title":"U"}`, "task.done", "", "completed_at"},
		{"mission done", "mission.add", `{"title":"M"}`, "mission.done", "", "completed_at"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := opstest.New(t, opstest.Fixed(at))
			id := h.ID(h.Must(c.add, c.in))
			if c.then != "" {
				h.Must(c.then, `{"id":`+opstest.Quote(id)+c.thenIn+`}`)
			}
			r := get(t, h, id)
			v, ok := r.Fields[c.field]
			if !ok {
				t.Fatalf("%s: no %s on %s", c.name, c.field, id)
			}
			got, err := time.Parse(time.RFC3339Nano, v.String())
			if err != nil {
				t.Fatalf("%s: %s=%q: %v", c.name, c.field, v.String(), err)
			}
			if !got.Equal(at) {
				t.Errorf("%s: %s = %v, want the engine clock %v", c.name, c.field, got, at)
			}
			if !r.HLC.Wall.Equal(at) {
				t.Errorf("%s: row HLC wall %v, want %v", c.name, r.HLC.Wall, at)
			}
		})
	}
}

// get reads a row back through the App.
func get(t *testing.T, h *opstest.H, raw string) row.Row {
	t.Helper()
	id, err := ids.ParseAny(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
