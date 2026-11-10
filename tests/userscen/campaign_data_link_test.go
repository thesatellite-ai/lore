//go:build userscen

package userscen

// campaign_data_link_test.go — `lore link add|list|remove`: a commit linked
// by HEAD, by full sha and by a short sha git cannot resolve; list and
// commit show filtered by the full sha, by the short sha git prints and in
// upper case; the duplicate refusal names the existing link; remove asks,
// keeps a tombstone and runs twice cleanly; REST twin of list.

import (
	"net/http"
	"strings"
	"testing"
)

// TestCampaignData_LinkShortSHA: `link list --commit` and `commit show`
// find a link by the 7- or 8-character sha a person copies from git log
// (they matched only the full 40-character sha).
func TestCampaignData_LinkShortSHA(t *testing.T) {
	c := resRepo(t)
	c.CommitAll("first")
	sha := c.Head()
	task := cdIDOf(t, c.Lore("task", "add", "ship it", "--to", "repo", "--commitment", "accepted", "--json"))
	link := cdIDOf(t, c.Lore("link", "add", "--entity="+task, "--commit=HEAD", "--json"))
	for _, f := range []string{sha, sha[:7], sha[:8], strings.ToUpper(sha[:10])} {
		if r := c.MustLore("link", "list", "--commit", f, "--json"); !strings.Contains(r.Stdout, link) {
			t.Errorf("link list --commit %s misses %s: %.300s", f, link, r.Stdout)
		}
	}
	for _, f := range []string{"0000000", "zzzzzzz", sha[:6]} {
		if r := c.MustLore("link", "list", "--commit", f, "--json"); strings.Contains(r.Stdout, link) {
			t.Errorf("link list --commit %s lists %s", f, link)
		}
	}
	// a short sha git cannot resolve is kept as given and found by itself
	other := cdIDOf(t, c.Lore("link", "add", "--entity="+task, "--commit=deadbeef", "--json"))
	if r := c.MustLore("link", "list", "--commit", "deadbeef", "--json"); !strings.Contains(r.Stdout, other) || strings.Contains(r.Stdout, link) {
		t.Errorf("link list --commit deadbeef: %.300s", r.Stdout)
	}
}

// TestCampaignData_LinkLifecycle: add → duplicate refused (naming the
// link) → list by entity → remove asks → remove → list → remove again.
func TestCampaignData_LinkLifecycle(t *testing.T) {
	c := resRepo(t)
	c.CommitAll("first")
	task := cdIDOf(t, c.Lore("task", "add", "ship it", "--to", "repo", "--commitment", "accepted", "--json"))
	link := cdIDOf(t, c.Lore("link", "add", "--entity="+task, "--commit="+c.Head(), "--json"))
	e := cdRefused(t, c.Lore("link", "add", "--entity="+task, "--commit=HEAD", "--json"), "E_INVALID_INPUT")
	if !strings.Contains(e.Hint, link) {
		t.Errorf("duplicate link: the hint does not name %s: %q", link, e.Hint)
	}
	for _, bad := range []struct{ args []string }{
		{[]string{"--entity=" + task, "--commit=not-a-ref"}},
		{[]string{"--entity=" + task}},
		{[]string{"--commit=HEAD"}},
	} {
		cdRefused(t, c.Lore(append(append([]string{"link", "add"}, bad.args...), "--json")...), "E_INVALID_INPUT")
	}
	list := func() string { return c.MustLore("link", "list", "--entity", task, "--json").Stdout }
	if !strings.Contains(list(), link) {
		t.Fatal("link not listed by entity")
	}
	cdRefused(t, c.Lore("link", "remove", link, "--json"), "E_NEEDS_CONFIRM")
	if !strings.Contains(list(), link) {
		t.Fatal("an unconfirmed remove removed the link")
	}
	c.MustLore("link", "remove", link, "--yes")
	if strings.Contains(list(), link) {
		t.Fatal("removed link still listed")
	}
	if r := c.Lore("link", "remove", link, "--yes", "--json"); r.Code == 0 {
		t.Errorf("removing a removed link succeeded: %s", r)
	} else if e := restCLIErr(r); e == nil || e.Exit != r.Code {
		t.Errorf("second remove: %s", r)
	}
	// the link may be added again after a remove
	c.MustLore("link", "add", "--entity="+task, "--commit=HEAD")
}

// TestCampaignData_LinkREST: the CLI and REST list the same links.
func TestCampaignData_LinkREST(t *testing.T) {
	c := resRepo(t)
	c.CommitAll("first")
	task := cdIDOf(t, c.Lore("task", "add", "ship it", "--to", "repo", "--commitment", "accepted", "--json"))
	c.MustLore("link", "add", "--entity="+task, "--commit=HEAD")
	srv := restStart(t, c)
	cli := c.MustLore("link", "list", "--json")
	if rep := srv.get(t, "/v1/links"); rep.status != http.StatusOK || !restJSONEqual([]byte(cli.Stdout), []byte(rep.body)) {
		t.Errorf("link list: CLI %.300s REST %d %.300s", cli.Stdout, rep.status, rep.body)
	}
}
