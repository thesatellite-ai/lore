package ask

// grow_test.go — devco D-1006 (bug hunt H-268): what ask prints in full
// is decided by bytes. A long note that fits whole next to the first
// candidate lines is printed whole, not cut to its best 1,000 characters
// with the answering sentence left out; a widened excerpt keeps the
// narrow cut's lines holding a question word it lacks; a long note off
// the question's subject stays cut.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestGrowPrintsFittingLongRowWhole: a 2,400-character note answers;
// the sentence after its matching section holds the answer but no word
// of the question. Cut to its best sections it was left out and the
// reader needed a lore show; it fits the 4,000 characters whole, so it
// is printed whole.
func TestGrowPrintsFittingLongRowWhole(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	body := longNote("As built: parcel desk", 2400,
		[2]string{"Label retry policy", "A failed label print at the parcel desk is retried by the label worker."},
		[2]string{"Escalation", "After the third failure the dock lead gets a page and the carrier pickup waits."},
	)
	d := data{rows: []textRow{
		{Table: "architecture_notes", ID: "anr_desk", Place: "kb/app", Title: "As built: parcel desk", Body: body, At: at, FactState: "live", Verified: "2026-11-20"},
	}}
	// rows enough that parcel and desk are common words and label, retry
	// and policy the question's rare ones (subject.go)
	for i, s := range fillerLines {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_desk%d", i), Place: "kb/app", Body: "At the parcel desk: " + s, At: at.Add(-time.Duration(i+1) * time.Hour)})
	}
	o := build(parseQuestion("what is the label retry policy of the parcel desk", now), d, 0, 0)
	text := o.Text()
	if len(o.Rows) == 0 || o.Rows[0].ID != "anr_desk" {
		t.Fatalf("rows %v: the note is not first:\n%s", rowIDs(o.Rows), text)
	}
	if o.Rows[0].FullChars != 0 || !strings.Contains(text, "dock lead gets a page") {
		t.Fatalf("the note fits whole but was cut (full_chars %d):\n%s", o.Rows[0].FullChars, text)
	}
	if body := o.body(); len(body) > DefaultMaxChars {
		t.Errorf("the answer is %d characters, over --max-chars %d", len(body), DefaultMaxChars)
	}
}

// TestGrowWidenKeepsQuestionWord: when a widened cut picks other
// sentences, the narrow cut's line holding a question word the wide cut
// lacks is kept; a line holding only words the wide cut prints is not.
func TestGrowWidenKeepsQuestionWord(t *testing.T) {
	t.Parallel()
	o := Out{q: parseQuestion("which bucket holds the dedup keys", now)}
	was := "The dedup keys live in the hot bucket.\n…\nKeys expire after a day."
	now := "Buckets are listed in the admin.\nKeys are written by the worker."
	got := o.dropped(now, was, []string{"bucket", "dedup", "keys"})
	if got != "The dedup keys live in the hot bucket." {
		t.Fatalf("dropped = %q, want the line holding dedup", got)
	}
	if got := o.dropped(now+"\nThe dedup step runs first.", was, []string{"bucket", "dedup", "keys"}); got != "" {
		t.Fatalf("dropped = %q, want none: the wide cut holds every question word", got)
	}
}

// TestGrowLongRowOffSubject: a long row whose printed part holds none of
// the question's rare words gets no wider slot; one holding a rare word
// does for the first slot, and for a later one only with a key word; a
// short row always does; with no subject terms (a store of fewer than
// rareFloor scored rows) a long row stays cut.
func TestGrowLongRowOffSubject(t *testing.T) {
	t.Parallel()
	o := Out{q: parseQuestion("where does the zebra label printer log", now)}
	st := &subjectTerms{weight: map[string]float64{}, rare: map[string]bool{"zebra": true}}
	long := strings.Repeat("The label printer logs to the dock journal. ", 40)
	off := Row{ID: "anr_off", Body: long, FullChars: 3000, Matched: []string{"zebra", "label"}, subject: st}
	if o.onSubject(off, true) {
		t.Errorf("a long row printing only common words is on the subject")
	}
	on := off
	on.Body = "The zebra printer logs to the dock journal. " + long
	if !o.onSubject(on, true) {
		t.Errorf("a long row printing the rare word zebra is off the subject")
	}
	nosub := on
	nosub.subject = nil
	if o.onSubject(nosub, true) {
		t.Errorf("a long row with no subject terms gets a wider slot")
	}
	short := Row{ID: "mem_short", Body: "The label printer logs to the dock journal.", Matched: []string{"label"}, subject: st}
	if !o.onSubject(short, true) || !o.onSubject(short, false) {
		t.Errorf("a short row is off the subject")
	}
	// a later slot (devco D-1035): a rare word is not enough, the row
	// must hold a key word of the question
	if o.onSubject(on, false) {
		t.Errorf("a long row holding a rare word that is not key took a later slot")
	}
	st.key = map[string]bool{"zebra": true}
	if !o.onSubject(on, false) {
		t.Errorf("a long row holding the key word zebra was refused a later slot")
	}
}

// TestGrowKeepsTop: grow never prints more rows in full than --top asks
// for, however much room the answer has left.
func TestGrowKeepsTop(t *testing.T) {
	t.Parallel()
	d := folderFixture()
	q := parseQuestion("how often does the invoice export retry a failed page", now)
	for _, top := range []int{1, 2} {
		o := build(q, d, top, 8000)
		n := 0
		for _, r := range o.Ranked {
			if r.Kind == KindRow {
				n++
			}
		}
		if n > top {
			t.Errorf("--top %d: %d rows printed in full:\n%s", top, n, o.Text())
		}
	}
}
