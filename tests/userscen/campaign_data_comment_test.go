//go:build userscen

package userscen

// campaign_data_comment_test.go — `lore comment add|list|search|delete`:
// odd bodies round-trip, search finds words in every script, a query that
// is not full-text syntax is refused with the parser's reason and a next
// step, delete → trash restore → purge with the list after each step, and
// the REST twin of list and search.

import (
	"net/http"
	"strings"
	"testing"
)

// TestCampaignData_CommentLifecycle: add (odd bodies) → list → search →
// delete (asks) → trash restore → delete --purge, list after each.
func TestCampaignData_CommentLifecycle(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "a memory to discuss")
	bodies := []string{"agreed; revisit in Q3", "שלום עולם review", "café ✓ 🚀 deploy", cdLong(), "see ${var:api_host} and select * from x"}
	var ids []string
	for _, b := range bodies {
		ids = append(ids, cdIDOf(t, c.Lore("comment", "add", "--on-table=memories", "--on-id="+m, "--body="+b, "--json")))
	}
	list := func() string { return c.MustLore("comment", "list", "--on-id="+m, "--json").Stdout }
	for _, id := range ids {
		if !strings.Contains(list(), id) {
			t.Fatalf("comment %s not listed", id)
		}
	}
	for _, q := range []struct{ query, want string }{
		{"revisit", ids[0]}, {"שלום", ids[1]}, {"café", ids[2]}, {"lorem", ids[3]}, {`"select"`, ids[4]},
	} {
		if r := c.MustLore("comment", "search", q.query, "--json"); !strings.Contains(r.Stdout, q.want) {
			t.Errorf("comment search %q misses %s: %.300s", q.query, q.want, r.Stdout)
		}
	}
	// refusals: no body, a missing target, a wrong-table id
	before := cdTreeHash(t, c)
	cdRefused(t, c.Lore("comment", "add", "--on-table=memories", "--on-id="+m, "--body=", "--json"), "E_INVALID_INPUT")
	// a flag-given id gets no next-step hint yet (the live not-found
	// hint reads only the first argument; see the campaign report)
	if r := c.Lore("comment", "add", "--on-table=memories", "--on-id=mem_01a1b7b91e947cd6802bd13a54250000", "--body=x", "--json"); r.Code != cdDocExit(t, "E_NOT_FOUND") || restCLIErr(r) == nil || restCLIErr(r).Code != "E_NOT_FOUND" {
		t.Errorf("comment on a missing row: %s", r)
	}
	if cdTreeHash(t, c) != before {
		t.Error("a refused comment add changed the tree")
	}
	cdRefused(t, c.Lore("comment", "delete", ids[0], "--json"), "E_NEEDS_CONFIRM")
	c.MustLore("comment", "delete", ids[0], "--yes")
	if strings.Contains(list(), ids[0]) {
		t.Fatal("deleted comment still listed")
	}
	cdRefused(t, c.Lore("comment", "delete", ids[0], "--yes", "--json"), "E_IN_TRASH")
	c.MustLore("trash", "restore", ids[0], "--yes")
	if !strings.Contains(list(), ids[0]) {
		t.Fatal("restored comment not listed")
	}
	c.MustLore("comment", "delete", ids[0], "--purge", "--confirm", "--yes")
	if strings.Contains(list(), ids[0]) {
		t.Error("purged comment still listed")
	}
	if r := c.Lore("trash", "restore", ids[0], "--yes", "--json"); r.Code == 0 {
		t.Errorf("a purged comment came back from the trash: %s", r)
	}
}

// TestCampaignData_CommentSearchSyntax: an FTS5 operator or a lone quote
// is refused as E_INVALID_INPUT naming the query and the parser's reason,
// with a hint (it said only "sqlite cache: search").
func TestCampaignData_CommentSearchSyntax(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "x")
	c.MustLore("comment", "add", "--on-table=memories", "--on-id="+m, "--body=and or not")
	for _, q := range []string{"AND", "OR", `"unclosed`, "NOT", "a AND"} {
		e := cdRefused(t, c.Lore("comment", "search", q, "--json"), "E_INVALID_INPUT")
		if !strings.Contains(e.Message, "not a valid search query (") || !strings.Contains(e.Message, q) || strings.TrimSpace(e.Hint) == "" {
			t.Errorf("comment search %q: %q / %q", q, e.Message, e.Hint)
		}
	}
	if r := c.MustLore("comment", "search", `"and"`, "--json"); !strings.Contains(r.Stdout, "cmt_") {
		t.Errorf("the quoted operator finds nothing: %s", r.Stdout)
	}
}

// TestCampaignData_CommentREST: list and search over the CLI and REST give
// the same document; the REST refusal of a bad query is the same error.
func TestCampaignData_CommentREST(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "rest")
	c.MustLore("comment", "add", "--on-table=memories", "--on-id="+m, "--body=shipped the fix")
	srv := restStart(t, c)
	cli := c.MustLore("comment", "list", "--json")
	if rep := srv.get(t, "/v1/comments"); rep.status != http.StatusOK || !restJSONEqual([]byte(cli.Stdout), []byte(rep.body)) {
		t.Errorf("comment list: CLI %.300s REST %d %.300s", cli.Stdout, rep.status, rep.body)
	}
	cliS := c.MustLore("comment", "search", "shipped", "--json")
	rep := srv.post(t, "/v1/comments/search", `{"query":"shipped"}`) // openapi.json: a POST
	if rep.status != http.StatusOK || !restJSONEqual([]byte(cliS.Stdout), []byte(rep.body)) {
		t.Errorf("comment search: CLI %.300s REST %d %.300s", cliS.Stdout, rep.status, rep.body)
	}
	bad := srv.post(t, "/v1/comments/search", `{"query":"AND"}`)
	if bad.err == nil || bad.err.Code != "E_INVALID_INPUT" || bad.status != http.StatusBadRequest {
		t.Errorf("REST bad query: %d %.300s", bad.status, bad.body)
	}
}
