package staleclaim

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// day is a synthetic row time: day n of a made-up month.
func day(n int) time.Time { return time.Date(2026, 3, n, 9, 0, 0, 0, time.UTC) }

// flagged is the ids Find flags, each with the row that contradicts it.
func flagged(docs []Doc) map[string]string {
	out := map[string]string{}
	for _, f := range NewIndex(docs, nil).FindAll() {
		out[f.ID] = f.By
	}
	return out
}

// TestStaleByContradiction is bug hunt H-289 (devco:D-1027): a row saying
// something is planned or not built yet is flagged when a newer row on
// the same subject says it shipped, and only then.
func TestStaleByContradiction(t *testing.T) {
	cases := []struct {
		name  string
		older Doc
		newer Doc
		flag  bool
	}{
		{"planned note, newer shipped note", Doc{Title: "Planned: retry queue for webhook deliveries"}, Doc{Title: "Retry queue for webhook deliveries shipped in v0.4"}, true},
		{"title names it, body says not built yet", Doc{Title: "Retry queue", Body: "Not built yet; planned for the third quarter."}, Doc{Title: "Retry queue", Body: "Shipped on the 3rd, behind no flag."}, true},
		{"fact_state planned, newer row says it is live", Doc{Title: "Retry queue", Body: "Webhook deliveries get a second try.", State: "planned"}, Doc{Title: "The retry queue is live for webhook deliveries."}, true},
		{"not implemented, then implemented", Doc{Title: "Acme export: CSV download is not implemented"}, Doc{Title: "Acme export CSV download implemented and merged"}, true},
		{"single identifier subject", Doc{Title: "TODO: retry_queue"}, Doc{Title: "retry_queue landed"}, true},
		{"newer row is older in time", Doc{Title: "Planned: retry queue for webhook deliveries"}, Doc{Title: "Retry queue for webhook deliveries shipped"}, false},
		{"another subject shipped", Doc{Title: "Planned: retry queue for webhook deliveries"}, Doc{Title: "Billing export shipped"}, false},
		{"one plain word shared", Doc{Title: "Planned: retry queue"}, Doc{Title: "Queue dashboard shipped"}, false},
		{"newer row says not yet", Doc{Title: "Planned: retry queue"}, Doc{Title: "Retry queue not shipped yet"}, false},
		{"newer row speaks of the future", Doc{Title: "Planned: retry queue"}, Doc{Title: "Retry queue will be released after the freeze"}, false},
		{"newer row says to be shipped", Doc{Title: "Planned: retry queue"}, Doc{Title: "Retry queue is to be shipped"}, false},
		{"newer row ships part, plans the rest", Doc{Title: "Planned: retry queue for email"}, Doc{Title: "Retry queue shipped for webhooks; retry queue for email still planned"}, false},
		{"older row says it shipped itself", Doc{Title: "Retry queue: planned in May, shipped in June"}, Doc{Title: "Retry queue shipped"}, false},
		{"newer row says unimplemented", Doc{Title: "Acme export: CSV download planned"}, Doc{Title: "Acme export CSV download still unimplemented"}, false},
		{"single plain word subject", Doc{Title: "TODO: webhooks"}, Doc{Title: "Webhooks shipped"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			older, newer := c.older, c.newer
			older.ID, older.At = "old", day(1)
			newer.ID, newer.At = "new", day(9)
			if c.name == "newer row is older in time" {
				newer.At = day(1).Add(-time.Hour)
			}
			got := flagged([]Doc{older, newer})
			if c.flag && got["old"] != "new" {
				t.Fatalf("%q then %q: want the older row flagged by the newer, got %v", older.Title+" "+older.Body, newer.Title+" "+newer.Body, got)
			}
			if !c.flag && len(got) != 0 {
				t.Fatalf("%q then %q: want no flag, got %v", older.Title+" "+older.Body, newer.Title+" "+newer.Body, got)
			}
		})
	}
}

// TestStaleClaimCommonWordsNameNoSubject: words most rows of a store hold
// are not a subject, so a store full of queue notes does not flag every
// planned queue note on the first shipped one.
func TestStaleClaimCommonWordsNameNoSubject(t *testing.T) {
	docs := []Doc{{ID: "old", Title: "Planned: retry queue", At: day(1)}, {ID: "new", Title: "Retry queue shipped", At: day(9)}}
	for i := range 40 {
		docs = append(docs, Doc{ID: fmt.Sprintf("n%02d", i), Title: fmt.Sprintf("Retry queue note %d about the acme widgets", i), At: day(2)})
	}
	if got := flagged(docs); len(got) != 0 {
		t.Fatalf("want no flag when retry and queue are in most rows, got %v", got)
	}
}

// TestStaleClaimNewestWins: the flag names the newest contradicting row,
// and Note prints its id and day.
func TestStaleClaimNewestWins(t *testing.T) {
	docs := []Doc{
		{ID: "old", Title: "Planned: retry queue for webhook deliveries", At: day(1)},
		{ID: "mid", Title: "Retry queue for webhook deliveries shipped", At: day(5)},
		{ID: "top", Title: "Retry queue for webhook deliveries released to everyone", At: day(8)},
	}
	got := NewIndex(docs, nil).Find([]string{"old", "mid", "missing"})
	if len(got) != 1 || got[0].By != "top" {
		t.Fatalf("want old flagged by top, got %+v", got)
	}
	if want := "stale: a newer row says it is done: top (2026-03-08)"; got[0].Note() != want {
		t.Fatalf("note = %q, want %q", got[0].Note(), want)
	}
	if !slices.Contains(got[0].Words, "retry") || !slices.Contains(got[0].Words, "queue") {
		t.Fatalf("words = %v, want retry and queue", got[0].Words)
	}
}
