package ask

import (
	"strings"
	"testing"
)

// TestHistoryNeverTheAnswer (devco D-820): a row superseded by another,
// past its valid_until, or replaced by a newer row of the same folder on
// the same thing is never presented as the current answer: the current
// row comes first and the old one, when shown, is marked as history.
func TestHistoryNeverTheAnswer(t *testing.T) {
	t.Parallel()
	d := fixture(t)
	row := func(id, place, at, body string) textRow {
		return textRow{Table: "memories", ID: id, Place: place, Body: body, At: day(t, at)}
	}
	g1 := row("mem_g1", "me/life", "2026-11-15", "My gym: ironworks gym.")
	g1.SupersededBy = "mem_g2" // superseded although it is the newer row
	g2 := row("mem_g2", "me/life", "2026-06-01", "My gym: peakfit gym.")
	c1 := row("mem_c1", "me/life", "2026-03-01", "My car insurance: harbourline.")
	c1.ValidUntil = day(t, "2026-11-01")
	c2 := row("mem_c2", "me/notes", "2026-02-01", "My car insurance: northstar mutual.")
	c2.ValidUntil = day(t, "2027-01-01") // still valid
	d.rows = append(d.rows, g1, g2, c1, c2)
	cases := []struct {
		q, first string
		history  map[string]string // id → the history mark it must carry
	}{
		{"how much money can lumen spend now", "mem_b2", map[string]string{"mem_b1": "history: replaced by mem_b2 from 2026-11-10"}},
		{"what are my dentist appointments now", "mem_l2", map[string]string{"mem_l1": "history: replaced by mem_l2 from 2026-11-01"}},
		{"what is my gym now", "mem_g2", map[string]string{"mem_g1": "history: superseded by mem_g2"}},
		{"who is my car insurance with", "mem_c2", map[string]string{"mem_c1": "history: valid until 2026-11-01"}},
	}
	for _, c := range cases {
		o := build(parseQuestion(c.q, now), d, 0, 0)
		if len(o.Rows) == 0 || len(o.Ranked) == 0 {
			t.Errorf("%q: no rows", c.q)
			continue
		}
		if o.Ranked[0].ID != c.first || o.Rows[0].ID != c.first || o.Rows[0].History != "" {
			t.Errorf("%q: first %s (rows[0] %s history %q), want the current row %s\n%s", c.q, o.Ranked[0].ID, o.Rows[0].ID, o.Rows[0].History, c.first, o.Text())
		}
		seen := map[string]bool{}
		for i, r := range o.Rows {
			want, old := c.history[r.ID]
			if !old {
				if r.History != "" && i == 0 {
					t.Errorf("%q: current row %s marked %q", c.q, r.ID, r.History)
				}
				continue
			}
			seen[r.ID] = true
			if r.History != want {
				t.Errorf("%q: %s history %q, want %q", c.q, r.ID, r.History, want)
			}
			if !strings.Contains(o.Text(), "["+want+"]") {
				t.Errorf("%q: text does not mark %s as history\n%s", c.q, r.ID, o.Text())
			}
		}
		for id := range c.history {
			if !seen[id] {
				t.Logf("%q: %s left out (fine: history only fills what top leaves)", c.q, id)
			}
		}
	}
	// the newest row says what it replaces
	o := build(parseQuestion("how much money can lumen spend now", now), d, 0, 0)
	if o.Rows[0].Note != "newest: it replaces mem_b1 from 2026-08-01" {
		t.Errorf("note %q", o.Rows[0].Note)
	}
}

// TestHistoryOnlyRowsStayMarked: when every row that answers is history
// (an invalidated memory and nothing newer), it is still shown, marked,
// and it does not lead the answer as its rows.
func TestHistoryOnlyRowsStayMarked(t *testing.T) {
	t.Parallel()
	d := data{rows: []textRow{{Table: "memories", ID: "mem_p1", Place: "me/life", Body: "My parking spot: level 3.", At: day(t, "2026-05-01"), ValidUntil: day(t, "2026-10-01")}}}
	o := build(parseQuestion("where is my parking spot", now), d, 0, 0)
	if len(o.Rows) != 1 || o.Rows[0].History != "history: valid until 2026-10-01" {
		t.Fatalf("rows %+v", o.Rows)
	}
}

// TestShortRowNeverReplacesLongNote (devco D-893, bug hunt H-153): a
// newer row of the same folder that only repeats the question's words
// does not replace an older row about something else or more: the long
// weekly note holding the move's date stays current and is never marked
// history of the one-line status, and of two short rows on different
// things in one folder neither replaces the other. A newer row that
// restates the older one still replaces it (the budget, the dentist).
func TestShortRowNeverReplacesLongNote(t *testing.T) {
	t.Parallel()
	var note strings.Builder
	note.WriteString("# Vela weekly sync\n\n")
	for _, s := range []string{"Hiring", "Support queue", "Design review"} {
		note.WriteString("## " + s + "\n\n")
		for range 6 {
			note.WriteString("The vela " + strings.ToLower(s) + " items were walked through in plain terms and everyone kept the current plan. ")
		}
		note.WriteString("\n\n")
	}
	note.WriteString("## Office move\n\nThe vela office move is booked for 2026-07-14 with the movers from Northfield.\n")
	row := func(id, at, body string) textRow {
		return textRow{Table: "memories", ID: id, Place: "me/work", Body: body, At: day(t, at)}
	}
	cases := []struct {
		name  string
		rows  []textRow
		q     string
		after map[string]string // id → the history it must carry ("" = current)
	}{
		{"long note, newer short status", []textRow{row("mem_n1", "2026-11-01", note.String()), row("mem_n2", "2026-11-10", "Vela status: the office move is on track.")},
			"when is the vela office move", map[string]string{"mem_n1": "", "mem_n2": ""}},
		{"two short rows, different things", []textRow{row("mem_o1", "2026-11-01", "Vela office move: booked for 2026-07-14 with the movers from Northfield."), row("mem_o2", "2026-11-10", "Vela status: the office move is on track.")},
			"when is the vela office move", map[string]string{"mem_o1": "", "mem_o2": ""}},
		{"a newer row restating the date replaces it", []textRow{row("mem_r1", "2026-11-01", "Vela office move: booked for 2026-07-14 with the movers from Northfield."), row("mem_r2", "2026-11-10", "Vela office move moved to 2026-08-02 (was 2026-07-14), same movers from Northfield.")},
			"when is the vela office move", map[string]string{"mem_r1": "history: replaced by mem_r2 from 2026-11-10", "mem_r2": ""}},
		{"the same label of two words replaces it", []textRow{row("mem_l1", "2026-11-01", "Vela status: waiting on the landlord to sign the lease for the new floor."), row("mem_l2", "2026-11-10", "Vela status (2026-11-10): the office move is on track.")},
			"what is the vela status", map[string]string{"mem_l1": "history: replaced by mem_l2 from 2026-11-10", "mem_l2": ""}},
		{"a one-word label (a name) does not", []textRow{row("mem_w1", "2026-11-01", "Vela: the office move needs the freight elevator reserved."), row("mem_w2", "2026-11-10", "Vela: the office move is on track.")},
			"vela office move", map[string]string{"mem_w1": "", "mem_w2": ""}},
	}
	for _, c := range cases {
		o := build(parseQuestion(c.q, now), data{rows: c.rows}, 0, 0)
		got := map[string]string{}
		for _, r := range o.Rows {
			got[r.ID] = r.History
		}
		for id, want := range c.after {
			h, ok := got[id]
			if !ok {
				t.Errorf("%s: %s not in the answer\n%s", c.name, id, o.Text())
				continue
			}
			if h != want {
				t.Errorf("%s: %s history %q, want %q\n%s", c.name, id, h, want, o.Text())
			}
		}
		if want := c.after[c.rows[0].ID]; want == "" && strings.Contains(o.Text(), "[history:") {
			t.Errorf("%s: text marks a current row as history\n%s", c.name, o.Text())
		}
	}
}

// TestLabelSplitOncePerRow: bug hunt H-213 (devco:D-954). markReplaced
// compares every pair of candidate rows; each row's label words are split
// once and kept on the row, not once per pair (~30 ms of a warm ask over
// 500 rows). A row's text never changes during one ask, so the kept label
// is the row's label.
func TestLabelSplitOncePerRow(t *testing.T) {
	r := &textRow{Body: "Status update lumen (2026-09-01): the move is on track."}
	first := labelOf(r)
	if strings.Join(first, " ") != "status update lumen" {
		t.Fatalf("label = %q, want status update lumen", first)
	}
	r.Body = "Budget orbit: changed under the row"
	if got := labelOf(r); strings.Join(got, " ") != "status update lumen" {
		t.Fatalf("labelOf split the row again (%q): the label is not kept on the row", got)
	}
}
