package ask

// longsec_test.go — devco D-969 (bug hunt H-228, Sync benchmark set 5
// asks 2 and 4): a long as-built architecture note whose one section
// answers the question lost to short decisions and memories that only
// share more of the question's words (S5-29, S5-33, S5-39), or ate the
// whole answer so the answering memory was not printed (S5-04). A long
// row now ranks by its best section (heading-bounded, scored on its own
// with its heading path), prints that section first marked "(matching
// section)", and never prints whole.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// fillerLines are sentences that share no word with the questions of
// this file: the body of a long note's other sections.
var fillerLines = []string{
	"The parcel desk prints zebra labels at the dock and carriers collect parcels twice daily.",
	"Invoices go out on the first weekday; the ledger closes when finance signs the monthly sheet.",
	"Theme colours come from the brand palette; the admin screens use the compact density.",
	"Image thumbnails are resized by the media worker into three widths and stored in buckets.",
	"Search synonyms are curated by merchandising and pushed to the index each night.",
	"Shipping zones map postcodes to carriers; a missing postcode falls back to the national zone.",
	"Translations live in locale files and the fallback locale is English for every storefront.",
	"Metrics are scraped every fifteen seconds and dashboards keep thirty days of history.",
}

// longNote is a markdown note: title, then each section as "## <head>"
// and its text, every other section padded with filler to about size
// characters.
func longNote(title string, size int, secs ...[2]string) string {
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	for _, s := range secs {
		b.WriteString("## " + s[0] + "\n\n" + s[1] + "\n\n")
	}
	out := b.String()
	for n := 0; len(out) < size; n++ {
		out += fmt.Sprintf("## Notes %d\n\n%s %s\n\n", n+1, fillerLines[n%len(fillerLines)], fillerLines[(n+3)%len(fillerLines)])
	}
	return out
}

// set5Case is one set-5 shaped question: the rows, and the row that answers.
type set5Case struct {
	name, q, want, fact string
	rows                []textRow
}

func set5Cases() []set5Case {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	note := func(id, title, body string) textRow {
		return textRow{Table: "architecture_notes", ID: id, Place: "kb/app", Title: title, Body: body, At: at, FactState: "live", Verified: "2026-11-20"}
	}
	mem := func(id, body string, h int) textRow {
		return textRow{Table: "memories", ID: id, Place: "kb/app", Body: body, At: at.Add(time.Duration(h) * time.Hour)}
	}
	dec := func(id, title, body string, h int) textRow {
		return textRow{Table: "decisions", ID: id, Place: "kb/app", Title: title, Body: body, At: at.Add(time.Duration(h) * time.Hour)}
	}
	return []set5Case{
		{
			// S5-33: decision, memory, decision shown; the note with the
			// review-decision section not
			name: "S5-33", q: "what stops a reviewer deciding on their own change and what is remaining",
			want: "anr_rev", fact: "ErrOwnApproval",
			rows: []textRow{
				note("anr_rev", "As built: approvals service", longNote("As built: approvals service", 5200,
					[2]string{"Overview", "The approvals service holds approval requests for catalog edits and price edits."},
					[2]string{"Storage", "Requests sit in one table with a status column; history rows are append only."},
					[2]string{"Reviewer self decision", "APPROVALS_ON turns the gate on. A reviewer deciding on their own change gets ErrOwnApproval. Remaining: bulk decisions and an audit export."},
				)),
				dec("dec_1", "Reviewers own their queue", "A reviewer stops a change that breaks the build; their own tests run first.", 1),
				mem("mem_1", "The remaining reviewer work: deciding who stops a stale change on their own.", 2),
				dec("dec_2", "Change review is remaining work", "Deciding the review rota is remaining; a reviewer can stop a change.", 3),
			},
		},
		{
			// S5-39: a note and a memory shown; the note with the boot
			// order section not
			name: "S5-39", q: "in which order does the container entrypoint run its boot steps and what is START_TOKEN for",
			want: "anr_boot", fact: "migrations, then the cache warm",
			rows: []textRow{
				note("anr_map", "As built: module map", longNote("As built: module map", 2600,
					[2]string{"Modules", "The container runs the web module, the worker module and the scheduler; each step of a job runs in the worker."},
					[2]string{"Order of modules", "Modules start in order: config, store, web; the entrypoint of each is its main package."},
				)),
				note("anr_boot", "As built: runtime", longNote("As built: runtime", 5600,
					[2]string{"Process model", "One process per container; signals go to the supervisor."},
					[2]string{"Entrypoint boot order and START_TOKEN", "The entrypoint runs migrations, then the cache warm, then the HTTP listener. START_TOKEN is a random id minted at boot and stamped on every log line of that boot."},
				)),
				mem("mem_boot", "The container entrypoint runs the boot steps in order and logs each step.", 1),
			},
		},
		{
			// S5-29: the module map note shown, the note with the catalog
			// row fields section not
			name: "S5-29", q: "which fields does a catalog row carry and which step fills the price fields",
			want: "anr_cat", fact: "price_cents",
			rows: []textRow{
				note("anr_cat", "As built: catalog sync", longNote("As built: catalog sync", 6000,
					[2]string{"Feed fetch", "The supplier feed is fetched hourly into object storage."},
					[2]string{"Catalog row fields", "A catalog row carries sku, title, price_cents, compare_at_cents, stock_qty and variant_options; the mapper step fills the price fields from the supplier price list."},
				)),
				dec("dec_cat", "Catalog rows stay flat", "Catalog rows carry flat fields; the step that fills a row never nests fields, and price fields stay integers.", 1),
				mem("mem_cat", "Which step fills the catalog fields: the import step fills every row field.", 2),
			},
		},
	}
}

// TestLongNoteLeavesRoom (S5-04): a long note whose every section
// repeats the question's words (no section to pick by its words) is cut
// to its best part, never printed whole, so the memory that answers is
// printed under it.
func TestLongNoteLeavesRoom(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	var b strings.Builder
	b.WriteString("# As built: job runner\n\n")
	for i := 0; len(b.String()) < 3800; i++ {
		fmt.Fprintf(&b, "## Job runner part %d\n\nIn this part a job is cancelled, abandoned or retried by the cli only through the runner. %s %s\n\n", i+1, fillerLines[i%len(fillerLines)], fillerLines[(i+5)%len(fillerLines)])
	}
	d := data{rows: []textRow{
		{Table: "architecture_notes", ID: "anr_job", Place: "kb/app", Title: "As built: job runner", Body: b.String(), At: at, FactState: "live", Verified: "2026-11-20"},
		{Table: "memories", ID: "mem_job", Place: "kb/app", Body: "A job is cancelled by cancel, abandoned after three missed beats and retried by retry; the cli acts as the cli actor.", At: at.Add(-time.Hour)},
	}}
	o := build(parseQuestion("how is a job cancelled, abandoned or retried by the cli", now), d, 0, 0)
	text := o.Text()
	if ids := rowIDs(o.Rows); !strings.Contains(strings.Join(ids, " "), "mem_job") || !strings.Contains(text, "cli actor") {
		t.Fatalf("rows %v: the memory that answers is not printed next to the long note:\n%s", ids, text)
	}
	if len(text) > 2500 {
		t.Errorf("the long note was printed whole (%d characters):\n%s", len(text), text)
	}
}

// TestLongNoteSectionRanks: the long note whose section answers ranks
// first and prints that section first, marked, holding the fact.
func TestLongNoteSectionRanks(t *testing.T) {
	t.Parallel()
	for _, c := range set5Cases() {
		o := build(parseQuestion(c.q, now), data{rows: c.rows}, 0, 0)
		text := o.Text()
		if ids := rowIDs(o.Rows); len(ids) == 0 || ids[0] != c.want {
			t.Errorf("%s: rows %v, want %s first:\n%s", c.name, ids, c.want, text)
			continue
		}
		if !strings.Contains(text, "(matching section)\n   "+"") || strings.Index(text, c.fact) > strings.Index(text, "(matching section)")+400 {
			t.Errorf("%s: the matching section is not printed first, marked:\n%s", c.name, text)
		}
		if !strings.Contains(text, c.fact) {
			t.Errorf("%s: the answer does not hold the section's fact %q:\n%s", c.name, c.fact, text)
		}
	}
}
