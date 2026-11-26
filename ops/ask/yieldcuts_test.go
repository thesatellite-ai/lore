package ask

// yieldcuts_test.go — devco D-1011 (bug hunt H-273): the first row's room
// comes before a later row's cut. Two long notes on the question's
// subject were each cut to about 1,000 characters next to five candidate
// lines, so the first note, the answering one, was printed first but its
// answering sentence did not fit its excerpt. The second note's cut now
// becomes a candidate line and the first note gets its room.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// yieldNote is a long as-built note on the parcel desk's label worker:
// sections each holding some of the question's words, the answer in the
// last one, padded to about size characters.
func yieldNote(title string, size int, secs ...[2]string) string {
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	for _, s := range secs {
		b.WriteString("## " + s[0] + "\n\n" + s[1] + "\n\n")
	}
	for n := 0; b.Len() < size; n++ {
		fmt.Fprintf(&b, "## Notes %d\n\n%s %s\n\n", n+1, fillerLines[n%len(fillerLines)], fillerLines[(n+3)%len(fillerLines)])
	}
	return b.String()
}

func yieldData() (data, string) {
	// each note is nine tenths of the default cap, so neither fits whole
	// next to the candidate lines (devco D-1013 raised the cap to 8,000)
	size := DefaultMaxChars * 9 / 10
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	const answer = "After three tries the job moves to the dead-letter tray and the dock lead gets a page."
	pad := " The night shift checks the dock board at every change of shift, and the board lists each station with its owner, its spare parts shelf and the phone number of the facilities desk that answers outside office hours."
	first := yieldNote("As built: label worker", size,
		[2]string{"Label worker queue", "The label worker reads print jobs from the label queue; each job names its printer, its template and its parcel." + pad},
		[2]string{"Label worker printers", "Every printer of the label worker has a heartbeat; a printer missing two heartbeats is skipped by the label queue." + pad},
		[2]string{"Label worker templates", "Label templates are versioned; the label worker renders a job with the template version the queue names." + pad},
		[2]string{"Label worker retries", answer},
	)
	second := yieldNote("As built: label printers", size,
		[2]string{"Label printers", "The label printers sit at the dock; the label worker sends them jobs over the print network." + pad},
		[2]string{"Firmware", "Printer firmware is updated by the facilities team on the first weekday of the month." + pad},
	)
	d := data{rows: []textRow{
		{Table: "architecture_notes", ID: "anr_worker", Place: "kb/app", Title: "As built: label worker", Body: first, At: at, FactState: "live", Verified: "2026-11-20"},
		{Table: "architecture_notes", ID: "anr_printers", Place: "kb/app", Title: "As built: label printers", Body: second, At: at.Add(-time.Hour), FactState: "live", Verified: "2026-11-20"},
	}}
	// rows enough that label, worker and queue are the question's rare
	// words (subject.go) and the candidate list has its five lines
	for i, w := range []string{"zones", "carriers", "scales", "pallets", "returns", "rates", "hours", "badges", "bins", "tapes"} {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_yield%d", i), Place: "kb/app",
			Body: fmt.Sprintf("The dock desk keeps its %s list in the shared sheet, tab %d, next to the open parcels, the dock worker rota and the carrier pickup times for the week.", w, i+1),
			At:   at.Add(-time.Duration(i+2) * time.Hour)})
	}
	for i, s := range fillerLines {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_fill%d", i), Place: "kb/app", Body: "At the parcel desk: " + s, At: at.Add(-time.Duration(i+20) * time.Hour)})
	}
	return d, answer
}

// TestYieldCutsGivesFirstRowTheRoom: the first note is printed with its
// answering sentence (whole or widened), the second note's cut is a
// candidate line, and the answer stays under --max-chars.
func TestYieldCutsGivesFirstRowTheRoom(t *testing.T) {
	t.Parallel()
	d, answer := yieldData()
	o := build(parseQuestion("how does the label worker handle a failed label", now), d, 0, 0)
	text := o.Text()
	if len(o.Rows) == 0 || o.Rows[0].ID != "anr_worker" {
		t.Fatalf("rows %v: the label worker note is not first:\n%s", rowIDs(o.Rows), text)
	}
	if !strings.Contains(text, answer) {
		t.Fatalf("the first note's answering sentence is not printed:\n%s", text)
	}
	for _, r := range o.Rows[1:] {
		if r.FullChars > 0 {
			t.Errorf("row %s is printed cut next to the cut first row:\n%s", r.ID, text)
		}
	}
	if !slices.ContainsFunc(o.Candidates, func(c Candidate) bool { return c.ID == "anr_printers" }) {
		t.Errorf("the second note is neither printed nor a candidate line:\n%s", text)
	}
	if body := o.body(); len(body) > DefaultMaxChars {
		t.Errorf("the answer is %d characters, over --max-chars %d", len(body), DefaultMaxChars)
	}
}

// TestYieldCutsKeepsWholeRows: a later row printed whole is not a cut,
// and a short cut row (whole at most yieldMin) is mostly on the page, so
// yieldCuts leaves both printed; with no long cut row after the first
// nothing changes, and a row of the now: line (one replacing another, or
// the one it replaces) stays.
func TestYieldCutsKeepsWholeRows(t *testing.T) {
	t.Parallel()
	o := Out{
		Rows:   []Row{{ID: "anr_a", FullChars: 5000}, {ID: "mem_b"}, {ID: "anr_c", FullChars: 4000}, {ID: "mem_d", FullChars: yieldMin}},
		Ranked: []Ranked{{KindRow, "anr_a"}, {KindRow, "mem_b"}, {KindRow, "anr_c"}, {KindRow, "mem_d"}},
	}
	if _, _, ok := o.yieldCuts("anr_a", nil, nil, nil, map[string]Candidate{}); !ok {
		t.Fatalf("a cut row after the first was not taken out")
	}
	if got := rowIDs(o.Rows); !slices.Equal(got, []string{"anr_a", "mem_b", "mem_d"}) {
		t.Errorf("rows %v, want anr_a, the whole mem_b and the short cut mem_d", got)
	}
	if o.Left != 1 || len(o.Ranked) != 3 {
		t.Errorf("left %d ranked %v, want 1 left out and three ranked", o.Left, o.Ranked)
	}
	if _, _, ok := o.yieldCuts("anr_a", nil, nil, nil, map[string]Candidate{}); ok {
		t.Errorf("no long cut row is left after the first, yet yieldCuts reports a change")
	}
	now := Out{
		Rows:   []Row{{ID: "anr_a", FullChars: 5000}, {ID: "anr_c", FullChars: 4000, replaces: "anr_old"}, {ID: "anr_old", FullChars: 4000}, {ID: "anr_e", FullChars: 4000}},
		Ranked: []Ranked{{KindRow, "anr_a"}, {KindRow, "anr_c"}, {KindRow, "anr_old"}, {KindRow, "anr_e"}},
	}
	if _, _, ok := now.yieldCuts("anr_a", nil, nil, nil, map[string]Candidate{}); !ok {
		t.Fatalf("the long cut anr_e was not taken out")
	}
	if got := rowIDs(now.Rows); !slices.Equal(got, []string{"anr_a", "anr_c", "anr_old"}) {
		t.Errorf("rows %v, want the now: line's anr_c and anr_old kept, anr_e taken out", got)
	}
}
