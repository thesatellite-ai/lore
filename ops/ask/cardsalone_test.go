package ask

// cardsalone_test.go — devco D-975 (bug hunt H-234, Sync benchmark set 6,
// S6-19): "why is the <payment> webhook receiver not mounted" was
// answered by three code_repo cards and no row, while search found the
// webhooks note, a memory and a task. The code_repos only shared a word
// with the question (a typo of their name, a part of it, a field value),
// yet the rows had to be about them, and full cards filled the cap. Now a
// resource counts as named only by its name or an alias said whole; the
// rows a question only touching resources holds answer by their words, a
// touched card prints as one line under them, and the cap never keeps the
// cards alone while a row matched.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// webhookRows are the rows that answer the S6-19 shape: an architecture
// note, a memory and a task; only the note says "webhooks", only the
// memory says neither Paygate nor webhooks.
func webhookRows() []textRow {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	return []textRow{
		{Table: "architecture_notes", ID: "anr_1", Place: "kb/app", Title: "As built: payment webhooks", Body: "Payment webhooks from Paygate arrive at the separate hookrcv service, not the API server; it verifies the signature and enqueues a job.", At: at},
		{Table: "memories", ID: "mem_1", Place: "kb/app", Body: "The receiver is not mounted in the API on purpose: it runs as its own binary built from Dockerfile.hookrcv.", At: at},
		{Table: "tasks", ID: "tsk_1", Place: "kb/app", Title: "Mount the Paygate receiver behind the gateway once the API splits", At: at},
	}
}

// codeRepos are code_repo resources named ns whose purpose repeats the
// question's words (the purpose is long, as the Sync cards are).
func codeRepos(ns ...string) []resourceRow {
	var rs []resourceRow
	for i, n := range ns {
		rs = append(rs, resourceRow{ID: fmt.Sprintf("res_%d", i), Place: "kb/org", Kind: "code_repo", Name: n, Fields: []fieldValue{
			{"purpose", "Receives Paygate webhooks and payment events for the product and hands them to the job queue; " + strings.Repeat("it records every event with its signature, retries and the run id. ", 16)},
			{"url", "https://git.example.test/" + n},
		}})
	}
	return rs
}

// TestTouchedResourcesNeverPushOutRows: resources the question only
// touches never decide which rows answer: by a typo (webhooks for
// webhook), a part of a name (webhook of payment_webhook_bridge) or a
// field value, the memory and the task that hold more of the question's
// words than the cards still answer, and the cards take at most one line
// under them.
func TestTouchedResourcesNeverPushOutRows(t *testing.T) {
	t.Parallel()
	const asked = "why is the Paygate webhook receiver not mounted"
	field := codeRepos("billing_core")
	field[0].Fields = append(field[0].Fields, fieldValue{"provider", "paygate"})
	for name, rs := range map[string][]resourceRow{
		"typo":  codeRepos("webhooks", "receivers", "mountd"),
		"part":  codeRepos("paygate_sdk", "paygate_bridge"),
		"field": field,
	} {
		q := parseQuestion(asked, now)
		q.home = "kb/app"
		o := build(q, data{resources: rs, rows: webhookRows()}, 0, 0)
		text := o.Text()
		var rows []string
		resBeforeRow, res := false, 0
		for _, r := range o.Ranked {
			switch r.Kind {
			case KindRow:
				rows = append(rows, r.ID)
				resBeforeRow = resBeforeRow || res > 0
			case KindResource:
				res++
			}
		}
		for _, want := range []string{"mem_1", "tsk_1", "anr_1"} {
			if !slices.Contains(rows, want) {
				t.Errorf("%s: row %s is not in the answer (rows %v):\n%s", name, want, rows, text)
			}
		}
		if resBeforeRow || touchedLines(text) > 1 {
			t.Errorf("%s: %d code_repo lines, a resource before a row %v; want at most one line, under the rows:\n%s", name, touchedLines(text), resBeforeRow, text)
		}
	}
}

// TestCardsNeverAnswerAlone: resources the question names whole lead the
// answer when no row holds a question word (every row changed), and
// their full cards filled the cap; the best row is now kept, cut, and
// the cards that do not fit are left out.
func TestCardsNeverAnswerAlone(t *testing.T) {
	t.Parallel()
	rs := codeRepos("hookrcv", "paygate_sdk", "billing_core")
	// each card line a third of the cap, less 20: the cards fit, no row
	// does after them
	for i := range rs {
		n := len(resourceOf(match{res: &rs[i], how: MatchName, whole: true}, rs, false).line())
		rs[i].Fields[0].Value += strings.Repeat("x", (DefaultMaxChars/3-20)-n)
	}
	rows := webhookRows()
	rows = append(rows, textRow{Table: "decisions", ID: "dec_1", Place: "kb/app", Title: "hookrcv, paygate_sdk and billing_core", Body: "hookrcv, paygate_sdk and billing_core ship together. " + strings.Repeat("Each release lists the three versions. ", 60), At: rows[0].At, SupersededBy: "dec_9"})
	q := parseQuestion("hookrcv paygate_sdk billing_core", now)
	o := build(q, data{resources: rs, rows: rows}, 0, 0)
	if !slices.ContainsFunc(o.Ranked, func(r Ranked) bool { return r.Kind == KindRow }) {
		t.Fatalf("the answer is the cards alone while a row matched: %+v\n%s", o.Ranked, o.Text())
	}
	if len(o.body()) > DefaultMaxChars {
		t.Errorf("the answer is %d characters, over the cap of %d:\n%s", len(o.body()), DefaultMaxChars, o.Text())
	}
}

// TestTypoOfPersonStillAnswers: a typo of a person's name only touches
// the question, yet the rows about the person still come first and
// everything about a typo of a name still leads with the card.
func TestTypoOfPersonStillAnswers(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := data{
		resources: []resourceRow{{ID: "res_1", Place: "kb/people", Kind: "person", Name: "alice-ng", Fields: []fieldValue{{"role", "designer"}}}},
		rows: []textRow{
			{Table: "decisions", ID: "dec_1", Place: "kb/app", Body: "alice-ng decided the budget for the launch: 40k.", At: at},
			{Table: "decisions", ID: "dec_2", Place: "kb/app", Body: "bob-li decided the budget for the offsite.", At: at},
		},
	}
	o := build(parseQuestion("what did alise-ng decide about the budget", now), d, 0, 0)
	if len(o.Rows) == 0 || o.Rows[0].ID != "dec_1" {
		t.Errorf("the row about alice-ng is not first:\n%s", o.Text())
	}
	about := build(parseQuestion("everything about alise-ng", now), d, 0, 0)
	if len(about.Ranked) == 0 || about.Ranked[0] != (Ranked{KindResource, "res_1"}) {
		t.Errorf("everything about a typo of a name does not lead with its card: %+v\n%s", about.Ranked, about.Text())
	}
}
