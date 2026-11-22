package ask

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// mem is a memory row written at day at (YYYY-MM-DD) in place.
func mem(t *testing.T, id, place, at, body string) textRow {
	t.Helper()
	return textRow{Table: "memories", ID: id, Place: place, Body: body, At: day(t, at)}
}

// TestDateIsAPriorNotAFilter (devco D-897, bug hunt H-150): a date phrase
// in a question with other words ranks the rows dated near it higher, it
// does not keep only them. "what certification did I complete last
// month" asked in November finds the note written this month (the
// window, October, holds only an unrelated row), and of two rows holding
// the words the one dated in the window comes first.
func TestDateIsAPriorNotAFilter(t *testing.T) {
	t.Parallel()
	d := data{rows: []textRow{
		mem(t, "mem_c1", "me/life", "2026-11-03", "Passed the cloud architect certification exam."),
		mem(t, "mem_o1", "me/life", "2026-10-12", "Repainted the hallway."),
	}}
	o := build(parseQuestion("what certification did I complete last month", now), d, 0, 0)
	if len(o.Rows) == 0 || o.Rows[0].ID != "mem_c1" {
		t.Fatalf("rows %v, want the certification note first\n%s", rowIDs(o.Rows), o.Text())
	}
	if !strings.HasPrefix(o.Text(), "dates: 2026-10-01 to 2026-10-31 (last month); rows dated near them rank higher\n") {
		t.Errorf("header does not say the date ranks rows:\n%s", o.Text())
	}

	d = data{rows: []textRow{
		mem(t, "mem_h1", "me/life", "2026-11-19", "Went hiking at the ridge trail."),
		mem(t, "mem_h2", "me/life", "2026-11-14", "Went hiking at the lake trail with Dana."),
		mem(t, "mem_x1", "me/life", "2026-11-15", "Baked bread."),
	}}
	o = build(parseQuestion("where did I go hiking last weekend", now), d, 0, 0)
	if got := rowIDs(o.Rows); len(got) < 2 || got[0] != "mem_h2" || got[1] != "mem_h1" {
		t.Fatalf("rows %v, want the weekend hike (2026-11-14) before the newer one\n%s", got, o.Text())
	}
	// a question that is only a date still lists the window's rows, oldest first
	o = build(parseQuestion("what happened last weekend", now), d, 0, 0)
	if got := rowIDs(o.Rows); !slices.Equal(got, []string{"mem_h2", "mem_x1"}) {
		t.Fatalf("what happened last weekend: rows %v, want the weekend's rows oldest first\n%s", got, o.Text())
	}
}

// TestDatePhrasesRead: the phrases windowOf does not read become a
// window, a named date stays a question word, the counting words go, and
// a phrase naming the future or a short month name used as a person's
// name reads as no window.
func TestDatePhrasesRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		q, since, until string // until "" = open; since "" with until "" = no window
		words           []string
	}{
		{"where did I go hiking last weekend", "2026-11-14", "2026-11-15", []string{"hiking", "weekend"}},
		{"what did I buy in february", "2026-02-01", "2026-02-28", []string{"buy", "february"}},
		{"what gift did I get on valentine's day", "2026-02-14", "2026-02-14", []string{"gift", "valentine"}},
		{"which course did I start 3 months ago", "2026-08-14", "2026-08-28", []string{"course", "start"}},
		{"what did I read over the past two weeks", "2026-11-07", "2026-11-21", []string{"read"}},
		{"what did I plant before march 3rd", "", "2026-03-02", []string{"plant", "march"}},
		{"what did I plant after march 3rd", "2026-03-04", "", []string{"plant", "march"}},
		{"what did I send to Jan", "", "", []string{"send", "jan"}},
		{"what is planned for this weekend", "2026-11-21", "2026-11-22", []string{"planned", "weekend"}},
		{"what is planned for next week", "", "", []string{"planned"}},
		{"what certification did I complete last month", "2026-10-01", "2026-10-31", []string{"certification", "complete"}},
	}
	for _, c := range cases {
		q := parseQuestion(c.q, now)
		since, until := "", ""
		if !q.window.Since.IsZero() {
			since = q.window.Since.Format(time.DateOnly)
		}
		if !q.window.End.IsZero() {
			until = q.window.End.AddDate(0, 0, -1).Format(time.DateOnly)
		}
		if since != c.since || until != c.until {
			t.Errorf("%q: window %s..%s (%s), want %s..%s", c.q, since, until, q.when, c.since, c.until)
		}
		if !slices.Equal(q.words, c.words) {
			t.Errorf("%q: words %v, want %v", c.q, q.words, c.words)
		}
	}
}

// TestRowWrittenThenIsCurrentThen: in a series of status updates each
// replaces the one before (markReplaced), but a question about two weeks
// ago is answered by the update written then, not marked history.
func TestRowWrittenThenIsCurrentThen(t *testing.T) {
	t.Parallel()
	d := data{rows: []textRow{
		mem(t, "mem_s1", "acme/status", "2026-10-28", "Status update lumen: design review done."),
		mem(t, "mem_s2", "acme/status", "2026-11-04", "Status update lumen: beta build shipped to ten users."),
		mem(t, "mem_s3", "acme/status", "2026-11-11", "Status update lumen: billing migration started."),
		mem(t, "mem_s4", "acme/status", "2026-11-18", "Status update lumen: billing migration finished."),
	}}
	o := build(parseQuestion("what was the lumen status update two weeks ago", now), d, 0, 0)
	if len(o.Rows) == 0 || o.Rows[0].ID != "mem_s2" || o.Rows[0].History != "" {
		t.Fatalf("rows %v, want mem_s2 (written 2026-11-04, the week asked about) first and current\n%s", rowIDs(o.Rows), o.Text())
	}
}

// TestWasFollowsTheAnswer: the row the answer replaced comes right after
// it, marked history, instead of after every other current row (and
// past --top); not for a file question or an everything-about answer.
func TestWasFollowsTheAnswer(t *testing.T) {
	t.Parallel()
	d := data{rows: []textRow{
		mem(t, "mem_b1", "acme/budget", "2026-08-01", "Budget for orbit is 40k for this year."),
		mem(t, "mem_b2", "acme/budget", "2026-11-10", "Budget for orbit moved to 55k (was 40k)."),
		mem(t, "mem_r1", "acme/eng", "2026-11-12", "Orbit review with finance went fine."),
		mem(t, "mem_r2", "acme/ops", "2026-11-13", "Orbit roadmap shared with the team."),
	}}
	o := build(parseQuestion("what is the orbit budget", now), d, 3, 0)
	got := rowIDs(o.Rows)
	if len(got) != 3 || got[0] != "mem_b2" || got[1] != "mem_b1" {
		t.Fatalf("rows %v, want mem_b2 then the row it replaced (mem_b1)\n%s", got, o.Text())
	}
	if o.Rows[1].History != "history: replaced by mem_b2 from 2026-11-10" {
		t.Errorf("mem_b1 history %q, want it marked replaced", o.Rows[1].History)
	}
}

// TestNotInMemoryHint: a name the question writes with a capital that no
// row holds starts the text with a hint line (and sets abstain in the
// JSON); the rows still print. A name a row holds, a capitalised first
// word or a month gives no hint.
func TestNotInMemoryHint(t *testing.T) {
	t.Parallel()
	d := data{rows: []textRow{
		mem(t, "mem_k1", "me/life", "2025-04-01", "Moved to a flat in Kyoto, near the river."),
		mem(t, "mem_k2", "me/life", "2026-01-10", "Lived in the flat for nine months now."),
	}}
	o := build(parseQuestion("how long have I lived in Shinjuku", now), d, 0, 0)
	if !o.Abstain.NotInMemory || !slices.Equal(o.Abstain.Missing, []string{"shinjuku"}) {
		t.Fatalf("abstain %+v, want shinjuku missing", o.Abstain)
	}
	if !strings.HasPrefix(o.Text(), "not in memory: no row holds shinjuku; the rows below are the nearest\n") || len(o.Rows) == 0 {
		t.Fatalf("text does not start with the hint or has no rows:\n%s", o.Text())
	}
	d.rows = append(d.rows, mem(t, "mem_w1", "me/life", "2026-02-01", "The flat's wifi password is on the fridge."))
	for _, q := range []string{"how long have I lived in Kyoto", "how long have I lived in Kyto", "Lived where in March", "how long have I lived in the flat", "what is the flat's Wi-Fi password"} {
		if o := build(parseQuestion(q, now), d, 0, 0); o.Abstain.NotInMemory || strings.HasPrefix(o.Text(), "not in memory") {
			t.Errorf("%q: abstains %+v\n%s", q, o.Abstain, o.Text())
		}
	}
}

// rowIDs is the ids of rows in order.
func rowIDs(rows []Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}
