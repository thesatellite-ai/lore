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
