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

// TestStaleAnswerSaysHowToReverify (devco:D-953, bug hunt H-212): an
// answer that prints a stale row, ranked or applied by the file's context
// (a question naming a file), says it is stale and how to clear it, once
// per row and only for a row it printed, in text and in --json.
func TestStaleAnswerSaysHowToReverify(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	fix := func(id string) string {
		return "when it still holds: lore meta set --id " + id + " --store kb/app --pair fact_verified_at=2026-11-23; when it does not: edit the row and its fact_evidence"
	}
	note := "stale: evidence changed since 2026-11-02 (svc/b.go)"
	rows := []textRow{
		{Table: "memories", ID: "mem_fresh", Place: "kb/app", Body: "quokka rule says exports run nightly", At: now.AddDate(0, 0, -2)},
		{Table: "memories", ID: "mem_stale", Place: "kb/app", Body: "quokka rule says refunds are capped", At: now.AddDate(0, 0, -1), Stale: note, Reverify: fix("mem_stale")},
	}
	out := build(parseQuestion("quokka rule", now), data{rows: rows}, DefaultTop, DefaultMaxChars)
	text := out.Text()
	for _, want := range []string{"[" + note + "]", "re-verify each stale row", "  mem_stale: " + fix("mem_stale") + "\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "mem_fresh: when") {
		t.Fatalf("a fresh row has a re-verify line:\n%s", text)
	}
	// the newer row replaces the older one on the same thing: the "now:"
	// line it leads with carries the mark too
	if !strings.Contains(text, "now: quokka rule says refunds are capped (changed ") || !strings.Contains(strings.SplitN(text, "\n", 2)[0], "["+note+"]") {
		t.Fatalf("the now: line of a stale row is not marked:\n%s", text)
	}
	for _, r := range out.Rows {
		if want := map[string]string{"mem_stale": fix("mem_stale")}[r.ID]; r.Reverify != want {
			t.Fatalf("--json reverify of %s: %q, want %q", r.ID, r.Reverify, want)
		}
	}

	// a row the file's context applied and the answer also ranks: one line
	out.Applies = []Applied{{ID: "mem_stale", Kind: "memory", Why: "words", Stale: note, Reverify: fix("mem_stale")}, {ID: "mem_cut", Stale: note, Reverify: fix("mem_cut")}}
	text = out.Text()
	if n := strings.Count(text, "  mem_stale: when"); n != 1 {
		t.Fatalf("want one re-verify line for mem_stale, got %d:\n%s", n, text)
	}
	if strings.Contains(text, "mem_cut") {
		t.Fatalf("a row the answer did not print has a re-verify line:\n%s", text)
	}
}
