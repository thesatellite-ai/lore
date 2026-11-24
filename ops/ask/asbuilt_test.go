package ask

// asbuilt_test.go — devco D-963 (Sync benchmark set 4, item 1): lore ask
// filled its 4,000-character answer with code_repo cards and loosely
// related rows of other folders, and never printed the as-built
// architecture note that held the answer (ask read no architecture
// notes at all). Now the notes of how the system is built are rows of
// ask's corpus, resources the question only touches print one short line
// each within a fifth of the cap, the repo's own folder ranks above
// another folder's with the same match, and a live fact above a planned
// one.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestAskReadsArchitectureNotes: the text statement reads the live rows of
// the architecture-note, cookbook-recipe and workflow tables (a
// workflow's title is its name), and leaves out an archived one, an
// incident's included.
func TestAskReadsArchitectureNotes(t *testing.T) {
	t.Parallel()
	run := textFixture(t)
	ctx := context.Background()
	for _, s := range []string{
		`INSERT INTO architecture_notes_all VALUES ('anr_1', 'kb/app', 'As built: job queue retries', 'a failed job is retried 5 times with backoff', '2026-11-20T00:00:00.000000000Z', NULL, NULL),
			('anr_2', 'kb/app', 'Old: job queue', 'archived queue note', '2026-11-01T00:00:00.000000000Z', '2026-11-02T00:00:00.000000000Z', NULL)`,
		`INSERT INTO cookbook_recipes_all VALUES ('rcp_1', 'kb/app', 'Replay a dead job', 'move it back from the dead-letter table', '2026-11-20T00:00:00.000000000Z', NULL, NULL)`,
		`INSERT INTO workflows_all VALUES ('wfl_1', 'kb/app', 'Release train', 'tag, build, publish', '2026-11-20T00:00:00.000000000Z', NULL, NULL)`,
		`INSERT INTO incidents_all VALUES ('inc_9', 'kb/app', 'archived outage', 'job queue stalled', '2026-11-20T00:00:00.000000000Z', '2026-11-21T00:00:00.000000000Z', NULL)`,
	} {
		if _, err := run(ctx, "CREATE TEMP TABLE IF NOT EXISTS _x(a); "+s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	res, err := run(ctx, textSQL)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range res.Rows {
		if cell(r[colKind]) == "row" {
			got[cell(r[colID])] = cell(r[colTitle])
		}
	}
	for id, title := range map[string]string{"anr_1": "As built: job queue retries", "rcp_1": "Replay a dead job", "wfl_1": "Release train"} {
		if got[id] != title {
			t.Errorf("ask's corpus has %s as %q, want title %q (rows: %v)", id, got[id], title, got)
		}
	}
	for _, id := range []string{"anr_2", "inc_9"} {
		if _, in := got[id]; in {
			t.Errorf("ask's corpus holds the archived row %s", id)
		}
	}
}

// syncFixture is the Sync-shaped store: the repo's own folder kb/app with
// an as-built architecture note and a planned memory, the company folder
// kb/org with ten code_repo cards whose purpose repeats the question's
// words, and another client's folder kb/other sharing a few words.
func syncFixture() data {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	var rs []resourceRow
	for i := range 10 {
		name := fmt.Sprintf("feed_part%d", i)
		rs = append(rs, resourceRow{ID: fmt.Sprintf("res_%d", i), Place: "kb/org", Kind: "code_repo", Name: name, Fields: []fieldValue{
			{"purpose", "Part of the feed product: job queue workers, retry handling, import and export feeds, admin screens, public URLs, webhooks and the worker fleet"},
			{"stack", "Go, Postgres, a job queue, a worker pool, retries with backoff, NATS, Redis, React admin"},
			{"url", "https://git.example.test/" + name},
		}})
	}
	return data{
		resources: rs,
		rows: []textRow{
			{Table: "architecture_notes", ID: "anr_1", Place: "kb/app", Title: "As built: feed import pipeline", Body: "The feed import runs in two steps: fetch the bulk file, then parse_and_ingest writes rows in batches of 500.", At: at, FactState: "live", Verified: "2026-11-20"},
			{Table: "memories", ID: "mem_plan", Place: "kb/app", Body: "Plan: the feed import pipeline work.", At: at.Add(3 * time.Hour), FactState: "planned"},
			{Table: "memories", ID: "mem_b", Place: "kb/other", Body: "Client B: their feed import pipeline runs nightly, after the backups, the cache warmers, the report jobs and the long tail of cleanup tasks of their old platform.", At: at.Add(2 * time.Hour)},
		},
	}
}

// TestTouchedResourcesBriefUnderRows: ten cards found by a part of their
// name (feed) print one short line each under the rows, within a fifth of
// the cap, and the as-built note leads; a card the question names whole
// keeps its full line.
func TestTouchedResourcesBriefUnderRows(t *testing.T) {
	t.Parallel()
	q := parseQuestion("how does the feed import pipeline work", now)
	q.home = "kb/app"
	o := build(q, syncFixture(), 0, 0)
	text := o.Text()
	if len(o.Ranked) == 0 || o.Ranked[0] != (Ranked{KindRow, "anr_1"}) {
		t.Fatalf("the answer does not lead with the as-built note: %+v\n%s", o.Ranked, text)
	}
	cards := 0
	for l := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(l, "code_repo ") {
			cards++
			if len(l) > 220 || strings.Contains(l, "stack") {
				t.Errorf("a card the question only touches prints more than one short line: %q", l)
			}
		}
	}
	if cards == 0 || cards*200 > DefaultMaxChars/resShare+200 || o.leftBrief == 0 {
		t.Errorf("cards %d (left %d): want a few one-line cards within a fifth of the cap:\n%s", cards, o.leftBrief, text)
	}
	if !strings.Contains(text, "more resources that only share a word with the question left out") || strings.Contains(text, "to stay under 4000") {
		t.Errorf("the left-out line does not say why the cards were left out:\n%s", text)
	}
	// named whole: the question is about it, its full line stays
	named := build(parseQuestion("how does feed_part3 import work", now), syncFixture(), 0, 0)
	if !strings.Contains(named.Text(), "stack Go") {
		t.Errorf("a card the question names whole lost its fields:\n%s", named.Text())
	}
}

// TestHomeAndFactStateRank: with the same words, the repo's own folder
// ranks above another client's, and a live fact above a planned one.
func TestHomeAndFactStateRank(t *testing.T) {
	t.Parallel()
	d := syncFixture()
	d.resources = nil
	d.rows[0].Table, d.rows[0].Title = "memories", "" // same shape as the others
	d.rows[0].Body = "The feed import pipeline fetches the bulk file, then writes rows in batches."
	q := parseQuestion("how does the feed import pipeline work", now)
	// a live fact first, a planned one last, whatever their age
	ids := rowIDs(build(q, d, 0, 0).Rows)
	t.Logf("first %v", ids)
	if len(ids) != 3 || ids[0] != "anr_1" || ids[2] != "mem_plan" {
		t.Errorf("rows %v: want the live fact first and the planned one last", ids)
	}
	// no fact states: the repo's folder (kb/app) before another client's
	// newer row
	for i := range d.rows {
		d.rows[i].FactState, d.rows[i].Verified = "", ""
	}
	q.home = "kb/app"
	ids = rowIDs(build(q, d, 0, 0).Rows)
	if len(ids) != 3 || ids[2] != "mem_b" {
		t.Errorf("rows %v: want both rows of the repo's folder before another client's", ids)
	}
}

// TestVerifiedBreaksTies: of two rows with the same match, the one
// verified more recently comes first.
func TestVerifiedBreaksTies(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := data{rows: []textRow{
		{Table: "memories", ID: "mem_old", Place: "kb/app", Body: "The feed import retries each batch.", At: at, Verified: "2026-10-01"},
		{Table: "memories", ID: "mem_new", Place: "kb/app", Body: "Each batch of the feed import retries.", At: at, Verified: "2026-11-20"},
	}}
	if ids := rowIDs(build(parseQuestion("does the feed import retry", now), d, 0, 0).Rows); len(ids) == 0 || ids[0] != "mem_new" {
		t.Errorf("rows %v: want the more recently verified row first", ids)
	}
}
