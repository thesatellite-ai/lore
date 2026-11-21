package ask

import (
	"maps"
	"testing"
)

// TestRowWordsSplitOnce (devco:D-847): an answer splits each row's text
// into words once and every later test of the row (its score, each topic
// of an everything-about answer, a window's subject check) reads that
// kept set. Before, "where does alice-ng work now" split each row up to
// six times, the one changed-fact question over the gate's 50 ms bar.
func TestRowWordsSplitOnce(t *testing.T) {
	t.Parallel()
	for _, q := range []string{"where does alice-ng work now", "everything about alice-ng", "what is the status of lumen", "what happened yesterday", "how much money can lumen spend now"} {
		d := fixture(t)
		got := build(parseQuestion(q, now), d, 0, 0).Text()
		if want := build(parseQuestion(q, now), fixture(t), 0, 0).Text(); got != want {
			t.Errorf("%q: a second answer differs\n got %s\nwant %s", q, got, want)
		}
		for _, r := range d.rows {
			if r.words != nil && !maps.Equal(r.words, tokensOf(r.Title+"\n"+r.Body)) {
				t.Errorf("%q: row %s keeps words %v, not its own text's", q, r.ID, r.words)
			}
		}
	}

	// the kept set is what every test reads: a row whose kept words hold
	// a budget word holds the budget topic, though its text does not
	budget, _ := topicOf("budget")
	r := textRow{ID: "mem_x", Body: "Lunch with alice-ng."}
	if holdsTopic(&r, budget) {
		t.Fatalf("fixture: %q holds the budget topic", r.Body)
	}
	r.words = map[string]bool{"budget": true}
	if !holdsTopic(&r, budget) {
		t.Errorf("holdsTopic split the text again instead of reading the row's kept words")
	}
	q := parseQuestion("what is the budget for lumen", now)
	if _, ok := score(q, nil, &r); !ok {
		t.Errorf("score split the text again instead of reading the row's kept words")
	}
	r.words = map[string]bool{"lunch": true, "with": true, "alice-ng": true}
	if _, ok := score(q, nil, &r); ok {
		t.Errorf("score answered from the text, not from the row's kept words")
	}
}
