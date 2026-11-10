//go:build userscen

package userscen

// campaign_query_followups_test.go — the owner's follow-ups to the query
// campaign (scen-query "For the owner"), driven through the built CLI the
// way a person and a script meet them: `lore search` leaves out '-'
// words, time_spent.task_deleted_at tells trashed time from live time
// under `lore sql --include-deleted`, and `lore show --json` is indented
// like every other --json.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestCampaignQueryFollowups_SearchNegation: "deploy -friday" drops the
// friday row (first, middle or with `--` in front), a '-' inside a word is
// a plain word, and a query of only '-' words exits non-zero with
// E_INVALID_INPUT and a hint naming a query that works; --help documents
// both the '-' and the emoji limit.
func TestCampaignQueryFollowups_SearchNegation(t *testing.T) {
	rw := newRestWorld(t)
	c := rw.repo
	fri := cqAddID(t, c, "memory", "add", "deploy on friday is risky")
	mon := cqAddID(t, c, "memory", "add", "deploy on monday")
	xray := cqAddID(t, c, "memory", "add", "well-known x-ray deploy")
	cases := []struct {
		args       []string
		want, gone []string
	}{
		{[]string{"search", "deploy -friday"}, []string{mon, xray}, []string{fri}},
		{[]string{"search", "--", "-friday deploy"}, []string{mon, xray}, []string{fri}},
		{[]string{"search", "deploy -friday -monday"}, []string{xray}, []string{fri, mon}},
		{[]string{"search", "well-known"}, []string{xray}, []string{fri, mon}},
		{[]string{"search", "deploy -x-ray"}, []string{fri, mon}, []string{xray}},
	}
	for _, tc := range cases {
		// --json before the query: after "--" every word is the query
		out := c.MustLore(append([]string{tc.args[0], "--json"}, tc.args[1:]...)...).Stdout
		var doc struct {
			Data struct {
				Hits []struct {
					ID string `json:"id"`
				} `json:"hits"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%v: %v in %q", tc.args, err, out)
		}
		got := map[string]bool{}
		for _, h := range doc.Data.Hits {
			got[h.ID] = true
		}
		for _, id := range tc.want {
			if !got[id] {
				t.Errorf("%v: %s missing: %s", tc.args, id, out)
			}
		}
		for _, id := range tc.gone {
			if got[id] {
				t.Errorf("%v: %s listed: %s", tc.args, id, out)
			}
		}
	}
	for _, q := range []string{"-friday", "-friday -monday"} {
		c.Lore("search", "--", q).Fails(t, cliExitUsage, "[E_INVALID_INPUT] search needs a word to search for",
			`hint: add a word to search for: lore search "deploy -friday"`)
	}
	c.Lore("search", "--help").OK(t).Says(t, "-word leaves out rows holding word", "Emoji are not indexed", `lore search "deploy -friday"`)
}

// TestCampaignQueryFollowups_TimeSpentTaskDeletedAt (scen-query OD-4
// setup): under `lore sql --include-deleted` a trashed task's time carries
// the time it went into the trash in task_deleted_at, a live task's time
// carries NULL, and without --include-deleted only the live task is
// listed, with NULL.
func TestCampaignQueryFollowups_TimeSpentTaskDeletedAt(t *testing.T) {
	rw := newRestWorld(t)
	c := rw.repo
	gone := cqAddID(t, c, "task", "add", "billable work", "--commitment", "accepted")
	live := cqAddID(t, c, "task", "add", "still going", "--commitment", "accepted")
	c.MustLore("task", "time", "log", gone, "1h")
	c.MustLore("task", "time", "log", live, "30m")
	c.MustLore("task", "delete", gone, "--yes")
	trashedAt := strings.TrimSpace(c.MustLore("sql", "run", "SELECT deleted_at FROM trash WHERE id = '"+gone+"'", "--format", "csv").Stdout)
	_, trashedAt, _ = strings.Cut(trashedAt, "\n") // drop the csv header
	if trashedAt == "" {
		t.Fatalf("the trash lists no deleted_at for %s", gone)
	}
	q := "SELECT task_id, coalesce(task_deleted_at, 'NULL'), seconds FROM time_spent ORDER BY task_id"
	all := c.MustLore("sql", "run", q, "--format", "csv", "--include-deleted").Stdout
	for _, want := range []string{gone + "," + trashedAt + ",3600", live + ",NULL,1800"} {
		if !strings.Contains(all, want) {
			t.Errorf("--include-deleted: want %q in\n%s", want, all)
		}
	}
	plain := c.MustLore("sql", "run", q, "--format", "csv").Stdout
	if strings.Contains(plain, gone) || !strings.Contains(plain, live+",NULL,1800") {
		t.Errorf("without --include-deleted:\n%s", plain)
	}
}

// TestCampaignQueryFollowups_ShowJSONIndented: `lore show --json` prints
// one indented JSON document (two-space indent, a trailing newline) like
// `lore memory show --json`, and it still decodes to the row.
func TestCampaignQueryFollowups_ShowJSONIndented(t *testing.T) {
	rw := newRestWorld(t)
	c := rw.repo
	id := cqAddID(t, c, "memory", "add", "indent me")
	for _, args := range [][]string{{"show", id, "--json"}, {"show", id, "--origin", "--json"}, {"memory", "show", id, "--json"}} {
		out := c.MustLore(args...).Stdout
		if !strings.HasPrefix(out, "{\n  \"") || !strings.HasSuffix(out, "\n}\n") {
			t.Errorf("%v: not indented JSON:\n%s", args, out)
		}
		if !json.Valid([]byte(out)) || !strings.Contains(out, id) {
			t.Errorf("%v: not the row's JSON:\n%s", args, out)
		}
	}
}

// sameJSON reports whether a and b are the same JSON document, whatever
// their whitespace (every --json output is indented; a test compares the
// document, not the bytes).
func sameJSON(a, b string) bool {
	var x, y json.RawMessage
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	var cx, cy bytes.Buffer
	return json.Compact(&cx, x) == nil && json.Compact(&cy, y) == nil && cx.String() == cy.String()
}
