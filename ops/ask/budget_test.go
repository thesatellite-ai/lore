package ask

// budget_test.go — devco D-1013 (bug hunt H-275): --max-chars defaults
// to 8,000 characters. At 4,000 an answer whose three answering notes
// are each about 2,000 characters long printed the first whole and the
// others cut or as candidate lines, so the reader needed a lore show for
// the sentence asked about.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestDefaultBudgetPrintsThreeLongRowsWhole: three as-built notes of
// about 2,000 characters each answer one question, each one part of it;
// with no --max-chars all three print whole, with the answering sentence
// of each, and the answer stays under the default cap.
func TestDefaultBudgetPrintsThreeLongRowsWhole(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	notes := []struct{ id, title, head, text, answer string }{
		{"anr_gate", "As built: parcel gateway", "Gateway drain", "The parcel gateway drains each tenant before the rollout moves on.", "Drained tenants are logged to the dock journal."},
		{"anr_queue", "As built: parcel queue", "Queue replay", "The parcel queue replays every held message after the rollout.", "Replays stop after the fifth failed attempt."},
		{"anr_cache", "As built: parcel cache", "Cache eviction", "The parcel cache evicts each shard once the rollout ends.", "Evicted shards are warmed again by the night job."},
	}
	d := data{}
	for i, n := range notes {
		body := longNote(n.title, 2000, [2]string{n.head, n.text}, [2]string{"Follow-up", n.answer})
		d.rows = append(d.rows, textRow{Table: "architecture_notes", ID: n.id, Place: "kb/app", Title: n.title, Body: body, At: at.Add(-time.Duration(i) * time.Minute), FactState: "live", Verified: "2026-11-20"})
	}
	// rows enough that parcel is a common word and gateway, queue and
	// cache the question's rare ones (subject.go)
	for i, s := range fillerLines {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_parcel%d", i), Place: "kb/app", Body: "At the parcel desk: " + s, At: at.Add(-time.Duration(i+1) * time.Hour)})
	}
	o := build(parseQuestion("how does the parcel rollout handle the gateway drain, the queue replay and the cache eviction", now), d, 0, 0)
	text := o.Text()
	whole := map[string]bool{}
	for _, r := range o.Rows {
		if r.FullChars == 0 {
			whole[r.ID] = true
		}
	}
	for _, n := range notes {
		if !whole[n.id] || !strings.Contains(text, n.answer) {
			t.Errorf("%s is not printed whole with %q (rows %v):\n%s", n.id, n.answer, rowIDs(o.Rows), text)
		}
	}
	if body := o.body(); len(body) > DefaultMaxChars {
		t.Errorf("the answer is %d characters, over --max-chars %d", len(body), DefaultMaxChars)
	}
}
