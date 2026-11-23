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

// TestGroupCommentsKeepStaleBelowFresh (devco:D-941 with D-942): a stale
// fact raised to its comment's place still ranks below a fresh row with
// the same match, and its comment stays directly under it; a fresh row
// raised by its comment keeps that place. Both rules hold together.
func TestGroupCommentsKeepStaleBelowFresh(t *testing.T) {
	t.Parallel()
	stale := "stale: evidence changed since 2026-11-02 (billing/refund.go)"
	rows := map[string]Hit{
		"mem_s": {Entity: "memory", ID: "mem_s", Title: "Refunds retry three times", Stale: stale},
		"dec_f": {Entity: "decision", ID: "dec_f", Title: "Payments use the gateway"},
	}
	hits := []Hit{
		comment("cmt_s", "mem_s", 3), // the stale row's comment, best match
		{Entity: "memory", ID: "mem_f", Score: 3},
		comment("cmt_f", "dec_f", 2.5), // a fresh row only its comment matched
		{Entity: "memory", ID: "mem_s", Score: 1, Stale: stale},
		{Entity: "memory", ID: "mem_w", Score: 1},
	}
	got := GroupComments(hits, parentsOf(rows))
	if want := "mem_s cmt_s mem_f dec_f cmt_f mem_w"; ids(got) == want {
		t.Fatalf("a stale row raised by its comment stands above a fresh row with the same match: %s", ids(got))
	}
	if want := "mem_f mem_s cmt_s dec_f cmt_f mem_w"; ids(got) != want {
		t.Fatalf("order:\n got %s\nwant %s", ids(got), want)
	}
	// the stale cut: 3*0.9 = 2.7 still above the fresh row raised to 2.5
	for i, h := range got {
		if h.CommentOn.ID != "" && (i == 0 || got[i-1].ID != h.CommentOn.ID) {
			t.Fatalf("comment %s is not directly under its row: %s", h.ID, ids(got))
		}
	}
}
