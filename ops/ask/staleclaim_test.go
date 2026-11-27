package ask

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestPlannedRowContradictedByNewerRow is bug hunt H-289 (devco:D-1027):
// an older row says the retry queue is planned, a newer row says it
// shipped. The answer marks the older row (text and --json, naming the
// newer row) and ranks it below the newer one; a planned row nothing
// contradicts stays unmarked, and no row is dropped.
func TestPlannedRowContradictedByNewerRow(t *testing.T) {
	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	rows := []textRow{
		{Table: "memories", ID: "mem_planned", Place: "repo", Body: "Planned: a retry queue for failed webhook deliveries (not built yet).", At: now.AddDate(0, 0, -24)},
		{Table: "memories", ID: "mem_shipped", Place: "repo", Body: "The retry queue for webhook deliveries shipped in v0.4.", At: now.AddDate(0, 0, -5)},
		{Table: "memories", ID: "mem_csv", Place: "repo", Body: "Planned: a retry button for the webhook deliveries page.", At: now.AddDate(0, 0, -4)},
	}
	c := corpusOfRows(rows)
	read := append([]textRow(nil), rows...)
	markContradicted(c, nil, read, nil)
	out := build(parseQuestion("is the webhook retry queue built", now), data{rows: read}, DefaultTop, DefaultMaxChars)
	at := map[string]int{}
	for i, r := range out.Rows {
		at[r.ID] = i
	}
	if len(out.Rows) != 3 {
		t.Fatalf("want all three rows, got %+v", out.Rows)
	}
	if at["mem_shipped"] > at["mem_planned"] {
		t.Fatalf("the shipped row must rank above the planned row it contradicts: %+v", out.Rows)
	}
	note := "stale: a newer row says it is done: mem_shipped (2026-03-20)"
	for _, r := range out.Rows {
		want, by := "", ""
		if r.ID == "mem_planned" {
			want, by = note, "mem_shipped"
		}
		if r.Stale != want || r.ContradictedBy != by {
			t.Fatalf("%s: stale %q contradicted_by %q, want %q %q", r.ID, r.Stale, r.ContradictedBy, want, by)
		}
	}
	if text := out.Text(); !strings.Contains(text, "["+note+"]") || strings.Contains(text, "re-verify each stale row") {
		t.Fatalf("text must carry the mark and no re-verify list:\n%s", text)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"contradicted_by":"mem_shipped"`) {
		t.Fatalf("--json has no contradicted_by: %s", raw)
	}
}

// TestHistoryRowNeitherMarkedNorMarks (devco:D-1027): a superseded row is
// printed as history already, so it is not marked, and a superseded
// "shipped" row does not mark a planned one.
func TestHistoryRowNeitherMarkedNorMarks(t *testing.T) {
	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	rows := []textRow{
		{Table: "memories", ID: "mem_planned", Place: "repo", Body: "Planned: a retry queue for failed webhook deliveries.", At: now.AddDate(0, 0, -24)},
		{Table: "memories", ID: "mem_shipped", Place: "repo", Body: "The retry queue for webhook deliveries shipped.", At: now.AddDate(0, 0, -5), SupersededBy: "mem_other"},
	}
	read := append([]textRow(nil), rows...)
	markContradicted(corpusOfRows(rows), nil, read, nil)
	for _, r := range read {
		if r.Stale != "" || r.NewerBy != "" {
			t.Fatalf("%s marked by a history row: %+v", r.ID, r)
		}
	}
}
