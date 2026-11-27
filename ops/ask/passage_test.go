package ask

// passage_test.go — bug hunt H-292 (devco D-1030, Sync benchmark set 9
// ask a): a long note with no headings ranks by its best passage, so the
// short row stating the answer in other words is no longer ranked under
// notes that hold more of the question's words scattered over several
// KB; and when the first row is cut, later rows holding far fewer of the
// question's words give it their room, while a row holding nearly as
// many keeps its place.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPassagesJoinParagraphsUpToSize(t *testing.T) {
	t.Parallel()
	para := strings.Repeat("a", 400)
	long := strings.Repeat("b", 400) + "\n" + strings.Repeat("c", 400) + "\n" + strings.Repeat("d", 400)
	got := passages(para+"\n\n"+para+"\n\n"+para+"\n\n"+long, 1000)
	var sizes []int
	for _, p := range got {
		sizes = append(sizes, len(p))
		if len(p) > 1000 {
			t.Errorf("passage of %d characters, want at most 1000", len(p))
		}
	}
	// two paragraphs fit one passage, the third starts the next with the
	// first line of the 1,202-character paragraph, split by its lines
	if len(got) != 3 || !strings.HasPrefix(got[1], "a") || !strings.Contains(got[1], "b") || !strings.HasPrefix(got[2], "c") {
		t.Fatalf("passages of sizes %v, want three: two paragraphs; one paragraph and the long one's first line; its other two lines", sizes)
	}
	if one := passages(strings.Repeat("e", 3000), 1000); len(one) != 1 {
		t.Errorf("a single 3,000-character line made %d passages, want 1", len(one))
	}
}

// scatteredNote is a note of about size characters written as
// paragraphs with no headings: one of words in every other paragraph,
// filler in the rest, so it holds every word somewhere but no two near
// each other.
func scatteredNote(lead string, words []string, size int) string {
	var b strings.Builder
	b.WriteString(lead + "\n\n")
	for n := 0; b.Len() < size || n < 2*len(words); n++ {
		if n%2 == 0 && n/2 < len(words) {
			fmt.Fprintf(&b, "Here the %s comes up once. ", words[n/2])
		}
		fmt.Fprintf(&b, "%s %s %s\n\n", fillerLines[n%len(fillerLines)], fillerLines[(n+2)%len(fillerLines)], fillerLines[(n+5)%len(fillerLines)])
	}
	return b.String()
}

const purgeAnswer = "Stale widget exports are removed by scripts/purge-exports.sh: it empties the archive bucket in eu-west-1 unless REGION is set."

func purgeFixture() data {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	words := []string{"tool", "purges", "stale", "widget", "exports", "archive", "bucket", "target", "default", "change"}
	d := data{rows: []textRow{
		{Table: "memories", ID: "mem_purge", Place: "kb/app", Body: purgeAnswer, At: at.Add(-48 * time.Hour)},
	}}
	for i, lead := range []string{"Ledger desk as built.", "Dock board as built.", "Media worker as built."} {
		d.rows = append(d.rows, textRow{Table: "architecture_notes", ID: fmt.Sprintf("anr_scattered%d", i), Place: "kb/app",
			Title: strings.TrimSuffix(lead, "."), Body: scatteredNote(lead, words, 7000), FactState: "built", At: at})
	}
	return d
}

func TestShortRowAboveNotesHoldingWordsApart(t *testing.T) {
	t.Parallel()
	d := purgeFixture()
	for _, r := range d.rows[1:] {
		if len(r.Body) <= passageMin || r.sectionsOf() != nil {
			t.Fatalf("fixture note %s: %d characters, sections %v; want over %d and no sections", r.ID, len(r.Body), r.sectionsOf() != nil, passageMin)
		}
	}
	q := parseQuestion("Which tool purges stale widget exports from the archive bucket, which region does it target by default, and how do I change the region?", now)
	o := build(q, d, DefaultTop, DefaultMaxChars)
	if len(o.Rows) == 0 || o.Rows[0].ID != "mem_purge" {
		t.Fatalf("rows %v, want mem_purge first: the notes hold more of the question's words, but never near each other:\n%s", rowIDs(o.Rows), o.Text())
	}
	if !strings.Contains(o.Text(), purgeAnswer) {
		t.Errorf("the answer is not printed:\n%s", o.Text())
	}
}

// labelFixture: an as-built note of about 4,500 characters holding every
// word of the label question, two short memories of about 2,000
// characters each naming closeWords, and short rows naming the dock.
func labelFixture(closeWords []string) (data, string) {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	const answer = "After three failed tries the label printer moves the label job to the dock station's retry tray."
	var note strings.Builder
	note.WriteString("# Label printer as built\n\n## Retries\n\n" + answer + "\n\n")
	for n := 0; note.Len() < 4500; n++ {
		fmt.Fprintf(&note, "## Notes %d\n\n%s %s\n\n", n+1, fillerLines[n%len(fillerLines)], fillerLines[(n+3)%len(fillerLines)])
	}
	d := data{rows: []textRow{
		{Table: "architecture_notes", ID: "anr_label", Place: "kb/app", Title: "Label printer as built", Body: note.String(), FactState: "built", At: at},
	}}
	// each memo on its own topic, none of the question's words in it
	topics := [][]string{
		{"The canteen opens at seven and closes after the late shift.", "Coffee beans are ordered from the roaster every second Monday.", "The kitchen rota hangs next to the fridge."},
		{"Payroll runs on the twenty-fifth of each month.", "Holiday requests go through the people team portal.", "Expense claims need a receipt photo and a cost centre."},
	}
	for i := range 2 {
		var b strings.Builder
		fmt.Fprintf(&b, "Memo %d: the %s. ", i+1, strings.Join(closeWords, " and the "))
		for n := 0; b.Len() < 2000; n++ {
			b.WriteString(topics[i][n%len(topics[i])] + " ")
		}
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_label%d", i+1), Place: "kb/app", Body: strings.TrimSpace(b.String()), At: at.Add(-time.Duration(i+1) * time.Hour)})
	}
	// rows enough that the note's words are the question's rare ones
	// (subject.go): each of these holds dock only
	for i, w := range []string{"zones", "carriers", "scales", "pallets", "returns", "rates", "hours", "badges"} {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_dock%d", i), Place: "kb/app",
			Body: fmt.Sprintf("The dock desk keeps its %s list in the shared sheet, tab %d.", w, i+1), At: at.Add(-time.Duration(i+10) * time.Hour)})
	}
	return d, answer
}

const labelQuestion = "how does the label printer retry a failed label job at the dock station"

func TestYieldRowsFarBelowGiveFirstRowItsRoom(t *testing.T) {
	t.Parallel()
	d, answer := labelFixture([]string{"label"})
	o := build(parseQuestion(labelQuestion, now), d, DefaultTop, DefaultMaxChars)
	text := o.Text()
	if len(o.Rows) == 0 || o.Rows[0].ID != "anr_label" {
		t.Fatalf("rows %v, want anr_label first:\n%s", rowIDs(o.Rows), text)
	}
	if o.Rows[0].FullChars != 0 {
		t.Fatalf("the first note was cut (whole %d characters) while memories holding one of its seven words took the room:\n%s", o.Rows[0].FullChars, text)
	}
	if !strings.Contains(text, answer) || len(text) > DefaultMaxChars {
		t.Errorf("answer missing or %d characters over %d:\n%s", len(text), DefaultMaxChars, text)
	}
}

func TestYieldRowsKeepsRowHoldingNearlyAsMany(t *testing.T) {
	t.Parallel()
	d, _ := labelFixture([]string{"label printer", "failed label job", "dock station"})
	o := build(parseQuestion(labelQuestion, now), d, DefaultTop, DefaultMaxChars)
	text := o.Text()
	if len(o.Rows) == 0 || o.Rows[0].ID != "anr_label" {
		t.Fatalf("rows %v, want anr_label first:\n%s", rowIDs(o.Rows), text)
	}
	for _, id := range []string{"mem_label1", "mem_label2"} {
		if !slices.ContainsFunc(o.Rows, func(r Row) bool { return r.ID == id && r.FullChars == 0 }) {
			t.Errorf("rows %v: %s, holding all but two of the question's words, left the answer for the first note's room:\n%s", rowIDs(o.Rows), id, text)
		}
	}
}
