package ask

// slots_test.go — devco D-1035 (bug hunt H-296, team-store benchmark set
// 10 asks a and c): a long note holding none of the question's key words
// gives up the first slot to a row holding one and nearly as many of the
// question's words, and a short row holding most of the question's words
// is printed whole before a second long note holding none of them.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// slotFixture: eight fillers (fillers in turn, %d for its number)
// holding the question's common words, so only zorblat tells the
// subject apart.
func slotFixture(fillers []string, rows ...textRow) data {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := data{rows: rows}
	for i := range 8 {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_fill%d", i), Place: "kb/app", At: at,
			Body: fmt.Sprintf(fillers[i%len(fillers)], i)})
	}
	return d
}

// sweepNote is a note of over shortRow characters holding the acme
// invoice sweep's common words (acme invoice sweep retry shop flag set
// refund), never zorblat.
func sweepNote(title string) string {
	var b strings.Builder
	b.WriteString(title + ". The acme invoice sweep and its retry, as built.\n\n")
	for n := 0; b.Len() <= shortRow+500; n++ {
		fmt.Fprintf(&b, "Stage %d of the acme invoice sweep keeps a cursor per shop, reads the flag set on it and logs each retry and refund to the desk journal.\n", n+1)
	}
	return b.String()
}

var (
	flagFiller   = []string{"Note %d: the acme invoice sweep runs a retry per shop and reads the flag set on it."}
	refundFiller = []string{"Note %d: the acme invoice sweep logs each retry.", "Note %d: the acme invoice sweep logs each refund."}
)

func rankOf(o Out, id string) int {
	return slices.IndexFunc(o.Ranked, func(it Ranked) bool { return it.Kind == KindRow && it.ID == id })
}

func TestLongNoteWithoutKeyWordYieldsFirstSlot(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := slotFixture(flagFiller,
		textRow{Table: "architecture_notes", ID: "anr_sweep", Place: "kb/app", Title: "Acme invoice sweep retry design",
			Body: sweepNote("Acme invoice sweep retry design"), FactState: "built", At: at},
		textRow{Table: "memories", ID: "mem_zorblat", Place: "kb/app", At: at,
			Body: "The invoice sweep skips a shop whose zorblat flag is set and logs the skip."},
	)
	q := parseQuestion("does the acme invoice sweep retry a shop whose zorblat flag is set", now)
	o := build(q, d, DefaultTop, DefaultMaxChars)
	if got := q.order["anr_sweep"]; got != 0 {
		t.Fatalf("fixture: search ranks the long note at %d, want first", got)
	}
	z, n := rankOf(o, "mem_zorblat"), rankOf(o, "anr_sweep")
	if z < 0 || (n >= 0 && n < z) {
		t.Fatalf("rows %v: the memory holding zorblat (the question's key word) is not before the long note holding none:\n%s", o.Cited, o.Text())
	}
	if !strings.Contains(o.Text(), "zorblat flag is set and logs the skip") {
		t.Errorf("the memory is not printed whole:\n%s", o.Text())
	}
}

func TestLongNoteKeepsFirstSlotOverRowHoldingFarFewerWords(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := slotFixture(flagFiller,
		textRow{Table: "architecture_notes", ID: "anr_sweep", Place: "kb/app", Title: "Acme invoice sweep retry design",
			Body: sweepNote("Acme invoice sweep retry design"), FactState: "built", At: at},
		// holds the key word but two words fewer than the note: a rare
		// word is not always the subject, the note stays first
		textRow{Table: "memories", ID: "mem_zorblat", Place: "kb/app", At: at, Body: "A zorblat flag marks a shop for the desk."},
	)
	q := parseQuestion("does the acme invoice sweep retry a shop whose zorblat flag is set", now)
	o := build(q, d, DefaultTop, DefaultMaxChars)
	if rankOf(o, "anr_sweep") != 0 {
		t.Fatalf("rows %v: the long note search ranks first lost its slot to a row holding far fewer of the question's words:\n%s", o.Cited, o.Text())
	}
}

func TestShortRowHoldingMostWordsBeforeSecondLongNote(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	const answer = "On refund the acme invoice sweep drops the retry and closes the shop's draft."
	d := slotFixture(refundFiller,
		textRow{Table: "decisions", ID: "dec_zorblat", Place: "kb/app", Title: "Zorblat refunds",
			Body: "The acme invoice sweep sends a zorblat refund through the desk with no retry.", At: at},
		textRow{Table: "architecture_notes", ID: "anr_sweep", Place: "kb/app", Title: "Acme invoice sweep retry design",
			Body: sweepNote("Acme invoice sweep retry design"), FactState: "built", At: at},
		textRow{Table: "memories", ID: "mem_refund", Place: "kb/app", At: at, Body: answer},
	)
	q := parseQuestion("what does the acme invoice sweep do with a retry on a zorblat refund", now)
	o := build(q, d, DefaultTop, DefaultMaxChars)
	if q.order["anr_sweep"] > q.order["mem_refund"] {
		t.Fatalf("fixture: search ranks the short row (%d) before the long note (%d)", q.order["mem_refund"], q.order["anr_sweep"])
	}
	s, n := rankOf(o, "mem_refund"), rankOf(o, "anr_sweep")
	if s < 0 || (n >= 0 && n < s) {
		t.Fatalf("rows %v: the short row holding most of the question's words is not before the second long note:\n%s", o.Cited, o.Text())
	}
	if !strings.Contains(o.Text(), answer) {
		t.Errorf("the short row is not printed whole:\n%s", o.Text())
	}
}

func TestKeyWordsAreTheQuestionsHeaviest(t *testing.T) {
	t.Parallel()
	var all []scored
	for i := range 10 {
		m := []string{"acme"}
		if i < 2 {
			m = append(m, "refund")
		}
		if i == 0 {
			m = append(m, "zorblat")
		}
		all = append(all, scored{matched: m})
	}
	st := subjectTermsOf(all, nil)
	for w, want := range map[string]bool{"zorblat": true, "refund": true, "acme": false} {
		if st.isKey(w) != want {
			t.Errorf("isKey(%q) = %v, want %v (weights %v)", w, !want, want, st.weight)
		}
	}
}
