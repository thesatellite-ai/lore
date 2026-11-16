package searchrank

import "testing"

// TestTitleSharePrefix (devco D-465): a prefix term (invoi*) is held by a
// title word starting with it, as a word term is by its forms; before,
// the '*' was dropped and "invoi" matched no title word (share 0).
func TestTitleSharePrefix(t *testing.T) {
	w := Of("invoi*")
	if len(w.Terms) != 1 || w.Terms[0] != "invoi*" {
		t.Fatalf("terms %v, want [invoi*]", w.Terms)
	}
	if got := TitleShare("Invoice numbering", w.Terms); got != 1 {
		t.Errorf("title share %v, want 1", got)
	}
	if got := TitleShare("Quarterly report", w.Terms); got != 0 {
		t.Errorf("title share %v, want 0", got)
	}
}

// TestOrderOnePath (D-465): words, phrases and prefixes rank a title hit
// above a body hit (given second in bm25 order) through the same Order.
func TestOrderOnePath(t *testing.T) {
	for _, q := range []string{"invoice", "invoi*", `"invoice numbering"`, "invoice numbering"} {
		w := Of(q)
		items := []Item{{Label: "Quarterly report", Matched: len(w.Terms)}, {Label: "Invoice numbering", Matched: len(w.Terms)}}
		got := w.Order(items, 10)
		if len(got) != 2 || got[0].Index != 1 || got[0].Score <= got[1].Score {
			t.Errorf("%q: order %+v, want the titled item first", q, got)
		}
	}
}

// TestPlanNatural: plain words are any-word, syntax as written.
func TestPlanNatural(t *testing.T) {
	if w := Of("how do we deploy orbit"); !w.Natural || len(w.Terms) != 2 {
		t.Errorf("natural plan %+v", w)
	}
	if w := Of("deploy AND orbit"); w.Natural || w.FTS != "deploy AND orbit" {
		t.Errorf("syntax plan %+v", w)
	}
}
