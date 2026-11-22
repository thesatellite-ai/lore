package ask

import (
	"testing"
)

// TestFusedOrderInsideABand (devco D-894, hunt H-149): lore's score
// counts the question words a row holds, so the one row holding the rare
// word ("parking") tied with three newer rows holding only the common one
// ("code") and, newest first, fell past --top 3. The meaning-and-words
// order (wsum50 of BM25 and the static vectors) weighs the rare word and
// puts that row first.
func TestFusedOrderInsideABand(t *testing.T) {
	t.Parallel()
	d := fixture(t)
	row := func(id, place, at, body string) textRow {
		return textRow{Table: "memories", ID: id, Place: place, Body: body, At: day(t, at)}
	}
	d.rows = append(d.rows,
		row("mem_park", "me/life", "2026-03-01", "Parking at the cabin: use the north lot, spot 12, gate opens with 4417."),
		row("mem_code1", "me/work", "2026-11-10", "Code freeze for the billing service starts Monday."),
		row("mem_code2", "me/work", "2026-11-11", "Code review checklist: tests, docs and the changelog."),
		row("mem_code3", "me/journal", "2026-11-12", "Wrote code all afternoon, then a long walk."),
	)
	o := build(parseQuestion("what is the parking code", now), d, 0, 0)
	if len(o.Rows) == 0 || o.Rows[0].ID != "mem_park" {
		t.Fatalf("first row %v, want mem_park (the only row holding the rare word) before the newer rows holding only \"code\"\n%s", firstID(o), o.Text())
	}
}

// TestFusedOrderLeavesHistoryATimeline: rows that a newer row replaced
// keep their newest-first order under the current answer (the fused
// order picks the answer, it does not reorder what came before it).
func TestFusedOrderLeavesHistoryATimeline(t *testing.T) {
	t.Parallel()
	o, text := ask(t, "what is the status of lumen")
	var hist []string
	for _, r := range o.Rows {
		if r.History != "" {
			hist = append(hist, r.ID)
		}
	}
	if len(hist) != 2 || hist[0] != "mem_s2" || hist[1] != "mem_s1" {
		t.Fatalf("history rows %v, want [mem_s2 mem_s1] (newest first)\n%s", hist, text)
	}
}

func firstID(o Out) string {
	if len(o.Rows) == 0 {
		return "(no row)"
	}
	return o.Rows[0].ID
}
