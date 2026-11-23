package search

import (
	"strings"
	"testing"
)

// parentsOf answers GroupComments from rows by id; out lists the ids
// read but out of scope.
func parentsOf(rows map[string]Hit, out ...string) CommentParent {
	return func(c CommentOn) (Hit, bool, bool) {
		h, ok := rows[c.ID]
		for _, o := range out {
			if o == c.ID {
				return h, ok, false
			}
		}
		return h, ok, ok
	}
}

// ids is the hit ids in order.
func ids(hits []Hit) string {
	s := make([]string, len(hits))
	for i, h := range hits {
		s[i] = h.ID
	}
	return strings.Join(s, " ")
}

// comment is a comment hit on the row on.
func comment(id, on string, score float64) Hit {
	return Hit{Entity: "comment", ID: id, Score: score, Body: "said about " + on, CommentOn: CommentOn{Table: "decisions", ID: on}}
}

// TestGroupCommentsNeverAboveTheirRow (devco D-942, H-191): a comment
// that scored above the row it is on is listed directly under that row,
// the row raised to the comment's place; a comment whose row did not
// match lists that row first (via_comment); a comment whose row is out of
// scope or gone keeps its place, labelled; a row that is history keeps
// its place after the current hits, its comment under it.
func TestGroupCommentsNeverAboveTheirRow(t *testing.T) {
	t.Parallel()
	dec := map[string]Hit{
		"dec_a": {Entity: "decision", ID: "dec_a", Title: "Use the gateway"},
		"dec_b": {Entity: "decision", ID: "dec_b", Title: "Billing runs nightly"},
		"dec_c": {Entity: "decision", ID: "dec_c", Title: "Out of scope"},
		"dec_h": {Entity: "decision", ID: "dec_h", Title: "Old plan", History: "superseded by dec_a"},
	}
	hits := []Hit{
		comment("cmt_1", "dec_a", 0.9), // above its row
		{Entity: "memory", ID: "mem_1", Score: 0.8},
		dec["dec_a"],
		comment("cmt_2", "dec_b", 0.7), // its row did not match
		comment("cmt_3", "dec_c", 0.6), // its row is out of scope
		comment("cmt_4", "dec_x", 0.5), // its row is gone
		comment("cmt_5", "dec_h", 0.45),
		dec["dec_h"], // history, after the current hits
	}
	dec["dec_a"] = Hit{Entity: "decision", ID: "dec_a", Title: "Use the gateway", Score: 0.4}
	got := GroupComments(hits, parentsOf(dec, "dec_c"))
	if want := "dec_a cmt_1 mem_1 dec_b cmt_2 cmt_3 cmt_4 dec_h cmt_5"; ids(got) != want {
		t.Fatalf("order:\n got %s\nwant %s", ids(got), want)
	}
	if got[3].ViaComment != "cmt_2" || got[3].Score != 0.7 || got[0].ViaComment != "" {
		t.Fatalf("via_comment: %+v / %+v", got[3], got[0])
	}
	if c := got[1].CommentOn; c.Entity != "decision" || c.Title != "Use the gateway" {
		t.Fatalf("label: %+v", c)
	}
	if c := got[6].CommentOn; c.Entity != "" || c.Title != "" || c.ID != "dec_x" {
		t.Fatalf("a gone row's label: %+v", c)
	}
	if c := got[5].CommentOn; c.Title != "Out of scope" {
		t.Fatalf("an out-of-scope row still labels its comment: %+v", c)
	}
}

// TestCommentHitText (D-942): a comment's line names the row it is on
// and marks it as history; a row listed for its comment says so; a list
// whose first row holds fewer words than the comment under it does not
// say that no row holds every word.
func TestCommentHitText(t *testing.T) {
	t.Parallel()
	out := RunOut{Data: RunData{Query: "gateway backoff", Hits: []Hit{
		{Entity: "decision", ID: "dec_a", Title: "Use the gateway", Partial: true, ViaComment: "cmt_1"},
		{Entity: "comment", ID: "cmt_1", Snippet: "we added <b>backoff</b> at the <b>gateway</b>", CommentOn: CommentOn{Table: "decisions", Entity: "decision", ID: "dec_a", Title: "Use the gateway"}},
	}}}
	text := out.Text()
	for _, want := range []string{
		`comment   cmt_1  on decision dec_a "Use the gateway" — we added backoff at the gateway  [comment: history]`,
		`decision  dec_a  Use the gateway  (listed for its comment cmt_1)`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "No row holds every word") {
		t.Fatalf("the comment holds every word:\n%s", text)
	}
}
