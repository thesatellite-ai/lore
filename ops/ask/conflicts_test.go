package ask

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// TestStatementsOf (bug hunt H-239): each value pattern a row states a
// field's value with is read, with the field and value normalised; plain
// words, prose after a colon, row ids and file names are not fields.
func TestStatementsOf(t *testing.T) {
	cases := []struct {
		text string
		want []string // key=canon
	}{
		{"The client's request_timeout defaults to 5000 ms; raise it.", []string{"request_timeout=5000ms"}},
		{"request_timeout has no default: every caller sets it.", []string{"request_timeout=<none>"}},
		{"There is no default for `request_timeout`.", []string{"request_timeout=<none>"}},
		{"set request-timeout = 5 s unless told otherwise", []string{"request_timeout=5000ms"}},
		{"requestTimeout: 5s", []string{"request_timeout=5000ms"}},
		{"--batch-size is 200 count", nil}, // no unit lore knows: no "is" statement
		{"the default of http.timeout is 30 s", []string{"http.timeout=30000ms"}},
		{"use a default 2 MB for upload.max_size", []string{"upload.max_size=2097152bytes"}},
		{"cache_ratio is 80% today", []string{"cache_ratio=80%"}},
		{"**sync_mode**: strict.", []string{"sync_mode=strict"}},
		{"sync_mode: the caller picks one per job", nil}, // prose, not a value
		{"timeout: 5 s", nil},                                       // a plain word is not a field
		{"see https://example.com/a_b: 3", nil},                     // inside a URL
		{"config.yaml: 3 lines changed", nil},                       // a file
		{"e.g: 5 s", nil},                                           // too short
		{"mem_0123456789abcdef0123: replaced by the new one", nil},  // a row id
		{"the export job and the request_timeout both matter", nil}, // shares the name, no value
	}
	for _, c := range cases {
		var got []string
		for _, s := range statementsOf(c.text) {
			got = append(got, s.key+"="+s.canon)
		}
		slices.Sort(got)
		got = slices.Compact(got)
		if !slices.Equal(got, c.want) {
			t.Errorf("statementsOf(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// conflictRows are three rows on one key: two disagree on its default,
// a third agrees with the first in other units, a fourth only names it.
func conflictRows(now time.Time) []textRow {
	return []textRow{
		{Table: "notes", ID: "not_arch", Place: "repo", Title: "gateway client architecture", Body: "The gateway client wraps every outbound call of the billing worker. It keeps a pool of eight connections per host, signs each request with the tenant key, and logs the span id. Its request_timeout defaults to 5000 ms and it retries twice on a 502 with jitter between 100 and 400 ms. Metrics go to the shared collector under the client prefix.", At: now.AddDate(0, 0, -20)},
		{Table: "hotfixes", ID: "hot_fix", Place: "repo", Title: "gateway client timeout hotfix", Body: "Since the hotfix there is no default for request_timeout in the gateway client: callers set it.", At: now.AddDate(0, 0, -3)},
	}
}

// TestAskPrintsConflictHint (bug hunt H-239, devco D-980): two printed
// rows stating different values for the same field get one conflict line
// in text, naming the field, both values and both rows with the command
// that shows each, and a conflicts entry in --json; the row with a live,
// newer verified fact leads.
func TestAskPrintsConflictHint(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	rows := conflictRows(now)
	rows[0].FactState, rows[0].Verified = "live", "2026-11-24" // the older row is the verified one
	rows[1].FactState = "planned"
	out := build(parseQuestion("gateway client request_timeout default", now), data{rows: rows}, DefaultTop, DefaultMaxChars)
	if len(out.Rows) != 2 {
		t.Fatalf("want both rows printed, got %+v", out.Rows)
	}
	if len(out.Conflicts) != 1 {
		t.Fatalf("want one conflict, got %+v", out.Conflicts)
	}
	c := out.Conflicts[0]
	if c.Field != "request_timeout" || len(c.Rows) != 2 || c.Rows[0] != (ConflictValue{ID: "not_arch", Value: "5000 ms"}) || c.Rows[1] != (ConflictValue{ID: "hot_fix", Value: "no default"}) {
		t.Fatalf("conflict = %+v", c)
	}
	text := out.Text()
	want := "  request_timeout: 5000 ms in not_arch, no default in hot_fix; lore show --id not_arch; lore show --id hot_fix\n"
	if !strings.Contains(text, "rows that disagree") || !strings.Contains(text, want) {
		t.Fatalf("text has no conflict line %q:\n%s", want, text)
	}
	// without fact keys the newer row leads
	out = build(parseQuestion("gateway client request_timeout default", now), data{rows: conflictRows(now)}, DefaultTop, DefaultMaxChars)
	if len(out.Conflicts) != 1 || out.Conflicts[0].Rows[0].ID != "hot_fix" {
		t.Fatalf("want the newer row first, got %+v", out.Conflicts)
	}
}

// TestAskNoConflictHint (bug hunt H-239): rows that agree in other units
// (5 s and 5000 ms), rows that only share the field's name, and a row the
// answer reads as history print no conflict line.
func TestAskNoConflictHint(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	q := parseQuestion("gateway client request_timeout default", now)
	agree := conflictRows(now)
	agree[1].Body = "Since the hotfix the gateway client request_timeout = 5 s unless a caller sets it."
	nameOnly := conflictRows(now)
	nameOnly[1].Body = "The hotfix logs the gateway client request_timeout of every call that fails."
	history := conflictRows(now)
	history[0].SupersededBy = "hot_fix"
	for name, rows := range map[string][]textRow{"agree": agree, "name only": nameOnly, "history": history} {
		out := build(q, data{rows: rows}, DefaultTop, DefaultMaxChars)
		if len(out.Rows) != 2 {
			t.Fatalf("%s: want both rows printed, got %+v", name, out.Rows)
		}
		if len(out.Conflicts) != 0 || strings.Contains(out.Text(), "rows that disagree") {
			t.Errorf("%s: want no conflict, got %+v\n%s", name, out.Conflicts, out.Text())
		}
	}
}
