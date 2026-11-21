package searchrank

import (
	"testing"
	"time"
)

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

// TestRetryPlainOnly (D-475): only plain words are retried quoted after a
// refusal; FTS5 syntax and text with no word keep it.
func TestRetryPlainOnly(t *testing.T) {
	for _, q := range []string{"c++", "how do we deploy orbit", "foo-bar"} {
		if !Of(q).Retry() {
			t.Errorf("%q: no retry, want plain words retried quoted", q)
		}
	}
	for _, q := range []string{`"`, `'`, `*`, `(`, `AND`, `a OR`, `NEAR(`, `"unclosed`, `NOT`, `a AND`} {
		if Of(q).Retry() {
			t.Errorf("%q: retried, want the refusal kept", q)
		}
	}
}

// TestOrderNamesFirst (devco D-791, owner P-18): a short row whose label
// opens with the words (an overview row "Alice Ng (person) — …", the
// resource alice_ng) outranks rows whose label only holds the words
// further on, even when bm25 put those first; rows that do not name the
// words keep their bm25 order.
func TestOrderNamesFirst(t *testing.T) {
	for _, q := range []string{"alice ng", "who is alice ng", "alice"} {
		w := Of(q)
		items := []Item{
			{Label: "Lunch with Alice Ng and the team, notes 1", Matched: len(w.Terms)},
			{Label: "Lunch with Alice Ng and the team, notes 2", Matched: len(w.Terms)},
			{Label: "Alice Ng (person) — CFO at Acme Co", Matched: len(w.Terms)},
			{Label: "alice_ng", Matched: len(w.Terms)},
		}
		got := w.Order(items, 10)
		if len(got) != 4 || got[0].Index != 2 || got[1].Index != 3 || got[2].Index != 0 || got[3].Index != 1 {
			t.Fatalf("%q: order %+v, want the overview row, the resource name, then bm25 order", q, got)
		}
		if got[1].Score <= got[2].Score {
			t.Errorf("%q: a naming row scores %.3f, a row only holding the words %.3f: want it higher", q, got[1].Score, got[2].Score)
		}
	}
	// one more matched word still outranks naming
	w := Of("alice ng budget")
	items := []Item{{Label: "Alice Ng (person) — CFO", Matched: 2}, {Label: "Lunch with Alice Ng about the budget", Matched: 3}}
	if got := w.Order(items, 10); got[0].Index != 1 {
		t.Errorf("order %+v: want the row holding every word first", got)
	}
}

// TestNames (D-791): the label's first words hold every word, in any
// order, with punctuation, '_' and '-' splitting words.
func TestNames(t *testing.T) {
	yes := map[string]string{
		"alice ng":   "Alice Ng (person) — CFO",
		"ng alice":   "Alice Ng",
		"alice":      "alice_ng",
		"orbit":      "# Orbit status: shipped",
		"well-known": "Well-known ports",
		"deploys":    "Deployment of orbit",
	}
	for q, label := range yes {
		if !Names(label, Of(q).Terms) {
			t.Errorf("Names(%q, %q) = false, want true", label, q)
		}
	}
	no := map[string]string{
		"alice ng": "Lunch with Alice Ng",
		"alice":    "Met Alice at lunch",
		"orbit":    "",
	}
	for q, label := range no {
		if Names(label, Of(q).Terms) {
			t.Errorf("Names(%q, %q) = true, want false", label, q)
		}
	}
}

// TestOrderNewestNamingFirst (D-791): among rows whose label names or
// holds every word equally the newest comes first (a fact that changed
// answers with its newest value); rows whose label holds only some of
// the words keep bm25 order.
func TestOrderNewestNamingFirst(t *testing.T) {
	w := Of("alice ng")
	old, recent := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	items := []Item{
		{Label: "Alice Ng (person) — CFO", Matched: 2, At: old},
		{Label: "Alice Ng (person) — now CEO", Matched: 2, At: recent},
		{Label: "Lunch with Alice Ng", Matched: 2, At: old},
		{Label: "Dinner with Alice Ng", Matched: 2, At: recent},
	}
	got := w.Order(items, 10)
	if got[0].Index != 1 || got[1].Index != 0 || got[2].Index != 3 || got[3].Index != 2 {
		t.Errorf("order %+v: want newest naming row, older naming row, then the newer row holding the words", got)
	}
	// "Orbit (project) — status: …" holds both words without naming them
	w = Of("orbit status")
	items = []Item{
		{Label: "Orbit (project) — status: on hold", Matched: 2, At: old},
		{Label: "Orbit (project) — status: shipped", Matched: 2, At: recent},
		{Label: "Orbit rollout notes: status", Matched: 2, At: old},
		{Label: "Orbit rollout notes", Matched: 2, At: old},
		{Label: "Orbit dashboards", Matched: 2, At: recent},
	}
	got = w.Order(items, 10)
	if got[0].Index != 1 || got[1].Index != 0 || got[2].Index != 2 || got[3].Index != 3 || got[4].Index != 4 {
		t.Errorf("order %+v: want the newest status row, the older one, the older row naming orbit status, then bm25 order", got)
	}
}
