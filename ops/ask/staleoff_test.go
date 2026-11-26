package ask

import (
	"fmt"
	"testing"
	"time"
)

// TestStaleOffTopicRowBelowFreshRareRow (devco:D-1007, set 8 ask 6): a
// stale row holding three of the question's common words ranked above a
// fresh row holding its two rare ones and took the first full slot, as in
// search. A stale row's words now count by their rarity (in search and
// here), so the fresh row answers first.
func TestStaleOffTopicRowBelowFreshRareRow(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	rows := []textRow{
		{Table: "memories", ID: "mem_fresh", Place: "repo", Body: "The quokka exporter writes one gzip file per tenant", At: now.AddDate(0, 0, -3)},
		{Table: "memories", ID: "mem_stale", Place: "repo", Body: "Billing uploads retry three times, then the failed batch waits for an operator", At: now.AddDate(0, 0, -1), Stale: "stale: evidence changed since 2026-11-02 (svc/b.go)"},
	}
	// the common words: many rows hold one of them
	for i, w := range []string{"uploads", "retry", "failed"} {
		for j := range 8 {
			rows = append(rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_fill%d%d", i, j), Place: "repo", Body: fmt.Sprintf("Note %d on %s for team %d", j, w, i), At: now.AddDate(0, 0, -10)})
		}
	}
	out := build(parseQuestion("does the quokka exporter retry failed uploads", now), data{rows: rows}, DefaultTop, DefaultMaxChars)
	pos := map[string]int{}
	for i, r := range out.Rows {
		pos[r.ID] = i + 1
	}
	if pos["mem_fresh"] != 1 {
		t.Fatalf("want mem_fresh first, got rows %v", ids(out))
	}
	if p := pos["mem_stale"]; p != 0 && p < pos["mem_fresh"] {
		t.Fatalf("the stale off-topic row ranks above the fresh one: %v", ids(out))
	}
}

// ids lists out's row ids in order.
func ids(out Out) []string {
	var s []string
	for _, r := range out.Rows {
		s = append(s, r.ID)
	}
	return s
}
