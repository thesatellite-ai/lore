package ask

// searchorder_test.go — devco D-984 (bug hunt H-243, owner decision "lore
// ask follows search"): ask's rows are search's top rows in search's
// order, with the inputs the gates prove kept (fact_state, as-built
// notes, a long row's best section, identifiers, topics, resource names).

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// TestSearchOrderRules: more of the question's words first; among rows
// holding as many, the one whose label holds more of them; equal labels
// keep search's kind order (architecture_notes before memories).
func TestSearchOrderRules(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	rows := []textRow{
		{Table: "hotfixes", ID: "hfx_c", Place: "kb/app", Title: "Signing failed", Body: "a deploy left the signing config empty", At: at},
		{Table: "decisions", ID: "dec_a", Place: "kb/app", Title: "Signing key", Body: "we rotate it every month", At: at},
		{Table: "memories", ID: "mem_b", Place: "kb/app", Body: "rotate the signing key every month", At: at},
	}
	q := parseQuestion("how do we rotate the signing key", now)
	order := searchOrder(q, nil, rows, nil)
	got := slices.SortedFunc(func(yield func(string) bool) {
		for id := range order {
			if !yield(id) {
				return
			}
		}
	}, func(a, b string) int { return order[a] - order[b] })
	if want := []string{"mem_b", "dec_a", "hfx_c"}; !slices.Equal(got, want) {
		t.Errorf("search order %v, want %v (words held, then title share)", got, want)
	}
}

// TestPhraseTermIsOneWord: a question naming E_ACME_QUOTA_MAX holds it
// only in a row writing that phrase, as search's FTS phrase does; a row
// holding acme, quota and max apart (E_ACME_QUOTA_EXCEEDED … max) does
// not get the word, nor its bm25.
func TestPhraseTermIsOneWord(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	rows := []textRow{
		{Table: "hotfixes", ID: "hfx_loose", Place: "kb/app", Title: "E_ACME_QUOTA_EXCEEDED after a config value was empty", Body: "raise the max of the acme quota", At: at},
		{Table: "memories", ID: "mem_exact", Place: "kb/app", Body: "ACME_QUOTA_MAX = 1150 caps the quotas one run handles", At: at},
	}
	order := searchOrder(parseQuestion("ACME_QUOTA_MAX default", now), nil, rows, nil)
	if order["mem_exact"] != 0 {
		t.Errorf("order %v: want the row writing ACME_QUOTA_MAX first", order)
	}
}

// TestLabeledNotesNotReplaced: two as-built notes of different subsystems
// in one folder share a store's common words; the newer one does not
// replace the older (markReplaced read the newer as holding half the
// older's words), so neither is printed as history.
func TestLabeledNotesNotReplaced(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	common := strings.Repeat("Every worker command prints the run it started; a failed job is retried with backoff. ", 20)
	d := data{rows: []textRow{
		{Table: "architecture_notes", ID: "anr_hook", Place: "kb/app", Title: "As built: hookrelay (webhook sender)", Body: "hookrelay sends webhooks. " + common, At: at},
		{Table: "architecture_notes", ID: "anr_rate", Place: "kb/app", Title: "As built: ratewall (rate limiter)", Body: "ratewall counts requests. " + common, At: at.Add(time.Hour)},
	}}
	o := build(parseQuestion("how does a worker command retry a failed job", now), d, 0, 0)
	if len(o.Rows) != 2 {
		t.Fatalf("rows %v: want both notes", rowIDs(o.Rows))
	}
	for _, r := range o.Rows {
		if r.History != "" {
			t.Errorf("%s printed as history (%s): a note of another subsystem is not a changed fact", r.ID, r.History)
		}
	}
}

// TestTypoNameReadsAsTheResource: a question naming a resource by a typo
// ranks the rows naming it as search would rank them for the name itself.
func TestTypoNameReadsAsTheResource(t *testing.T) {
	t.Parallel()
	ms := []match{{res: &resourceRow{Name: "alice-ng"}, said: []string{"alise-ng"}}}
	if got := resourceTerms([]string{"alise-ng", "budget"}, ms); !slices.Equal(got, []string{"alice-ng", "budget"}) {
		t.Errorf("terms %v: want the typo read as alice-ng", got)
	}
}
