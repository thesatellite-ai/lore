package ask

// named_test.go — devco D-1037 (bug hunt H-297, team-store benchmark set
// 10 ask b): a row the answer prints that names another row by id brings
// it in: printed after it when it holds a word of the question and fits,
// else candidate 1; never a loop, a missing id skipped quietly.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeID is an id in lore's format (prefix, _, 32 hex) made from n.
func fakeID(prefix string, n int) string {
	return fmt.Sprintf("%s_019a0000000070008000%012x", prefix, n)
}

const rejoinQ = "when may an acme app rejoin its workspace after it deleted its own data"

// rejoinFixture: a note holding every word of rejoinQ (search's first
// row, printed) whose text is note, twenty fillers holding most of the
// question's words, and the extra rows (the rows the note names).
func rejoinFixture(note string, extra ...textRow) data {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := data{rows: []textRow{{Table: "memories", ID: fakeID("mem", 1), Place: "kb/app", At: at, Body: note}}}
	for i := range 20 {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fakeID("mem", 100+i), Place: "kb/app", At: at,
			Body: fmt.Sprintf("Note %d: the acme app keeps its workspace data and may rejoin it later.", i+1)})
	}
	d.rows = append(d.rows, extra...)
	return d
}

func TestNamedRowHoldingAQuestionWordIsPrinted(t *testing.T) {
	t.Parallel()
	dec := fakeID("dec", 2)
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	const rule = "An app comes back only after it removed itself, capped by its previous grant."
	for _, cite := range []string{"${row:" + dec + "}", dec, "`" + dec + "`"} {
		d := rejoinFixture("The acme app registry keeps one install per workspace; an app that deleted its own data may rejoin. Rules for rejoining: "+cite+".",
			textRow{Table: "decisions", ID: dec, Place: "kb/app", Title: "Comeback cap", Body: rule, At: at})
		o := build(parseQuestion(rejoinQ, now), d, DefaultTop, DefaultMaxChars)
		if rankOf(o, fakeID("mem", 1)) < 0 {
			t.Fatalf("fixture (%s): the note naming the decision is not printed:\n%s", cite, o.Text())
		}
		if !strings.Contains(o.Text(), rule) || rankOf(o, dec) < 0 {
			t.Errorf("cited as %s: the decision the printed note names is not printed (candidates %v):\n%s", cite, candIDs(o.Candidates), o.Text())
		}
		if n := rankOf(o, dec); n >= 0 && n != rankOf(o, fakeID("mem", 1))+1 {
			t.Errorf("cited as %s: the decision is printed at %d, not right after the note naming it (%v)", cite, n, o.Cited)
		}
		if len(o.Text()) > DefaultMaxChars {
			t.Errorf("cited as %s: the answer is %d characters, over --max-chars %d", cite, len(o.Text()), DefaultMaxChars)
		}
	}
}

func TestNamedRowWithoutQuestionWordsIsCandidateOne(t *testing.T) {
	t.Parallel()
	dec := fakeID("dec", 2)
	d := rejoinFixture("The acme app registry keeps one install per workspace; an app that deleted its own data may rejoin. Rules: ${row:"+dec+"}.",
		textRow{Table: "decisions", ID: dec, Place: "kb/app", Title: "Comeback cap", Body: "Comes back only after it removed itself, capped by its previous grant."})
	// the decision holds no word of the question: only the corpus holds it
	d.corp = corpusOfRows(d.rows)
	d.rows = d.rows[:len(d.rows)-1]
	o := build(parseQuestion(rejoinQ, now), d, DefaultTop, DefaultMaxChars)
	if rankOf(o, dec) >= 0 {
		t.Fatalf("the decision holding none of the question's words is printed in full:\n%s", o.Text())
	}
	if len(o.Candidates) == 0 || o.Candidates[0].ID != dec {
		t.Fatalf("candidates %v: the decision the printed note names is not candidate 1:\n%s", candIDs(o.Candidates), o.Text())
	}
	if !strings.Contains(o.Candidates[0].Line, "capped by its previous grant") && !strings.Contains(o.Candidates[0].Title, "Comeback cap") {
		t.Errorf("candidate 1 %+v does not show the decision's title or sentence", o.Candidates[0])
	}
}

func TestNamedRowsNeverLoopAndMissingIDsAreSkipped(t *testing.T) {
	t.Parallel()
	note, dec, gone := fakeID("mem", 1), fakeID("dec", 2), fakeID("dec", 3)
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := rejoinFixture("The acme app registry keeps one install per workspace; an app that deleted its own data may rejoin. See "+note+", ${row:"+dec+"} and ${row:"+gone+"}.",
		textRow{Table: "decisions", ID: dec, Place: "kb/app", Title: "Comeback cap", At: at,
			Body: "An app comes back only after it removed itself (" + dec + "); the registry note is " + note + "."})
	o := build(parseQuestion(rejoinQ, now), d, DefaultTop, DefaultMaxChars)
	for _, id := range []string{note, dec} {
		if n := strings.Count(strings.Join(o.Cited, " "), id); n != 1 {
			t.Errorf("%s is cited %d times, want once: %v", id, n, o.Cited)
		}
	}
	if slices.Contains(o.Cited, gone) || slices.Contains(candIDs(o.Candidates), gone) {
		t.Errorf("the id of no row (%s) is in the answer: cited %v, candidates %v", gone, o.Cited, candIDs(o.Candidates))
	}
}

func TestNamedIDs(t *testing.T) {
	t.Parallel()
	a, b := fakeID("dec", 2), fakeID("mem", 3)
	text := "Rules: ${row:" + a + "}; also " + b + ", again " + a + ". Not x" + b + ", not " + b + "9, not " + strings.ToUpper(a) + "."
	got := namedIDs(text)
	if !slices.Equal(got, []string{a, b}) {
		t.Errorf("namedIDs = %v, want [%s %s]", got, a, b)
	}
	if got := namedIDs(a); !slices.Equal(got, []string{a}) {
		t.Errorf("namedIDs of the bare id = %v", got)
	}
}

func TestIndexNoteNamingManyRowsLeadsWithOne(t *testing.T) {
	t.Parallel()
	ids := make([]string, 5)
	var cites []string
	var named []textRow
	for i := range ids {
		ids[i] = fakeID("dec", 10+i)
		cites = append(cites, ids[i])
		body := fmt.Sprintf("Release step %d is signed off by the desk.", i+1)
		if i == 3 {
			body = "Release step 4 moves the workspace to the new desk."
		}
		named = append(named, textRow{Table: "decisions", ID: ids[i], Place: "kb/app", Title: fmt.Sprintf("Step %d", i+1), Body: body})
	}
	d := rejoinFixture("The acme app registry keeps one install per workspace; an app that deleted its own data may rejoin. Index: "+strings.Join(cites, ", ")+".", named...)
	// the named rows are not read for the question: only the corpus holds them
	d.corp = corpusOfRows(d.rows)
	d.rows = d.rows[:len(d.rows)-len(named)]
	base := build(parseQuestion(rejoinQ, now), data{rows: d.rows}, DefaultTop, DefaultMaxChars)
	o := build(parseQuestion(rejoinQ, now), d, DefaultTop, DefaultMaxChars)
	got := 0
	for _, c := range o.Candidates {
		if slices.Contains(ids, c.ID) {
			got++
		}
	}
	if got != 1 || len(o.Candidates) == 0 || o.Candidates[0].ID != ids[3] {
		t.Fatalf("candidates %v: want only the named row holding a question word (%s) at the head", candIDs(o.Candidates), ids[3])
	}
	// search's next rows keep their places after it, minus what the line's room took
	if n := len(base.Candidates); n > 1 && o.Candidates[1].ID != base.Candidates[0].ID {
		t.Errorf("search's first candidate %s is not right after the named row: %v", base.Candidates[0].ID, candIDs(o.Candidates))
	}
}
