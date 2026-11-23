package ask

import (
	"strings"
	"testing"
	"time"
)

// TestStaleRowRanksBelowFreshWithSameMatch (devco:D-941, bug hunt H-190):
// of two rows matching the question alike, the newer one states a fact
// whose evidence changed after it was verified (markStale): the fresh row
// comes first, and the answer marks the stale one in --json and text.
func TestStaleRowRanksBelowFreshWithSameMatch(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	note := "stale: evidence changed since 2026-11-02 (svc/b.go)"
	rows := []textRow{
		{Table: "memories", ID: "mem_fresh", Place: "repo", Body: "quokka rule says exports run nightly and take forty minutes", At: now.AddDate(0, 0, -2)},
		{Table: "memories", ID: "mem_stale", Place: "repo", Body: "quokka rule says refunds go back to the original card", At: now.AddDate(0, 0, -1), Stale: note},
	}
	q := parseQuestion("quokka rule", now)
	out := build(q, data{rows: rows}, DefaultTop, DefaultMaxChars)
	if len(out.Rows) != 2 || out.Rows[0].ID != "mem_fresh" || out.Rows[1].ID != "mem_stale" {
		t.Fatalf("want mem_fresh then mem_stale, got %+v", out.Rows)
	}
	if out.Rows[0].Stale != "" || out.Rows[1].Stale != note {
		t.Fatalf("stale marks: %+v", out.Rows)
	}
	if text := out.Text(); !strings.Contains(text, "["+note+"]") {
		t.Fatalf("text has no stale mark:\n%s", text)
	}
}

// TestFactSQLIsFileOnly: the fact keys statement depends only on the
// cache files, so the read server keeps its answer like textSQL's.
func TestFactSQLIsFileOnly(t *testing.T) {
	if !FileOnly(factSQL) {
		t.Fatal("factSQL must be FileOnly")
	}
}
