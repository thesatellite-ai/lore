//go:build userscen

package userscen

// campaign_data_followups_test.go — follow-ups of the data-area campaign,
// each the way an operator meets it through the built binary: resource
// text values expand under --expand (and only then), external sources
// refuse a duplicate name and take a name where they took an id, every
// delete or remove asks unless --yes, the resource size limit names what
// it counts, an id given by flag gets the same not-found hint as a
// positional one, `resource kind add --to me`, and singular/plural texts.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestCampaignDataFollowups_ResourceExpand: a ${var:…} in a text value is
// filled in by `resource show --expand` and `lore show --id res_… --expand`,
// stays as written without the flag, and a sensitive text value is never
// expanded (with or without --expand).
func TestCampaignDataFollowups_ResourceExpand(t *testing.T) {
	c := resRepo(t)
	c.MustLore("resource", "kind", "add", "host")
	c.MustLore("resource", "field", "add", "host", "note", "--type", "text")
	c.MustLore("resource", "field", "add", "host", "hidden", "--type", "text", "--sensitive")
	c.MustLore("vars", "set", "region", "eu")
	id := cdIDOf(t, c.Lore("resource", "add", "host", "web1", "--set", "note=in ${var:region}", "--set", "hidden=at ${var:region}", "--json").OK(t))

	type showOut struct {
		Data     resJSON           `json:"data"`
		Expanded map[string]string `json:"expanded"`
	}
	plain := c.Lore("resource", "show", "host/web1").OK(t).Stdout
	if strings.Contains(plain, "in eu") || strings.Contains(plain, "expanded:") {
		t.Errorf("resource show without --expand expanded:\n%s", plain)
	}
	got := c.Lore("resource", "show", "host/web1", "--expand").OK(t).Stdout
	if !strings.Contains(got, "note: in eu") || strings.Contains(got, "at eu") {
		t.Errorf("resource show --expand:\n%s", got)
	}
	j := th.DecodeJSON[showOut](t, c.Lore("resource", "show", "host/web1", "--expand", "--json").OK(t))
	if j.Expanded["note"] != "in eu" || j.Data.Values["note"] != "in ${var:region}" {
		t.Errorf("resource show --expand --json: expanded %v, values %v", j.Expanded, j.Data.Values)
	}
	if _, leaked := j.Expanded["hidden"]; leaked {
		t.Errorf("a sensitive value was expanded: %v", j.Expanded)
	}

	type rowOut struct {
		Expanded map[string]string `json:"expanded"`
	}
	if r := th.DecodeJSON[rowOut](t, c.Lore("show", id, "--json").OK(t)); len(r.Expanded) != 0 {
		t.Errorf("lore show --id without --expand expanded: %v", r.Expanded)
	}
	r := th.DecodeJSON[rowOut](t, c.Lore("show", id, "--expand", "--json").OK(t))
	if r.Expanded["fields.note"] != "in eu" || len(r.Expanded) != 1 {
		t.Errorf("lore show --expand --json: %v", r.Expanded)
	}
	if txt := c.Lore("show", id, "--expand").OK(t).Stdout; !strings.Contains(txt, "fields.note: in eu") || strings.Contains(txt, "at eu") {
		t.Errorf("lore show --expand:\n%s", txt)
	}
}

// TestCampaignDataFollowups_ExternalSourceNames: a second source of a
// name is refused like a second tag (E_NAME_TAKEN naming the existing
// id), and enable/disable take the unique name as well as the ext_ id; an
// unknown name is E_NOT_FOUND naming the list command.
func TestCampaignDataFollowups_ExternalSourceNames(t *testing.T) {
	c := resRepo(t)
	c.MustLore("external-source", "add", "--name", "issues", "--kind", "github")
	id := strings.Fields(c.Lore("external-source", "list").OK(t).Stdout)[3]
	e := cdRefused(t, c.Lore("external-source", "add", "--name", "issues", "--kind", "gitlab", "--json"), "E_NAME_TAKEN")
	if !strings.Contains(e.Message, id) || !strings.Contains(e.Hint, id) {
		t.Errorf("duplicate refusal names no existing id %s: %+v", id, e)
	}
	if n := strings.Count(c.Lore("external-source", "list").OK(t).Stdout, "issues"); n != 1 {
		t.Fatalf("the refused add wrote a row: %d sources named issues", n)
	}
	for _, tc := range []struct{ verb, by, want string }{
		{"enable", "issues", "[on] issues"},
		{"disable", "issues", "[off] issues"},
		{"enable", id, "[on] issues"},
		{"disable", id, "[off] issues"},
	} {
		c.MustLore("external-source", tc.verb, tc.by)
		if got := c.Lore("external-source", "list").OK(t).Stdout; !strings.Contains(got, tc.want) {
			t.Errorf("%s %s: list says\n%s", tc.verb, tc.by, got)
		}
	}
	cdRefused(t, c.Lore("external-source", "enable", "nope", "--json"), "E_NOT_FOUND")
}

// TestCampaignDataFollowups_DeletesAsk: one confirm rule. Without a
// terminal and without --yes every delete or remove answers
// E_NEEDS_CONFIRM with a hint and changes nothing; with --yes it runs. A
// name or id that does not exist is reported first, never asked about.
func TestCampaignDataFollowups_DeletesAsk(t *testing.T) {
	c := resRepo(t)
	c.MustLore("resource", "kind", "add", "host")
	c.MustLore("resource", "field", "add", "host", "note", "--type", "text")
	c.MustLore("resource", "field", "add", "host", "extra", "--type", "text")
	c.MustLore("vars", "set", "region", "eu")
	c.MustLore("resource", "add", "host", "web1", "--set", "note=old")
	for _, tc := range []struct {
		name string
		args []string
		gone func() bool // true once the delete happened
	}{
		{"vars rm", []string{"vars", "rm", "region"}, func() bool {
			return !strings.Contains(c.Lore("vars", "list").OK(t).Stdout, "region")
		}},
		{"resource delete --where below the preview count", []string{"resource", "delete", "--kind", "host", "--where", "note=old"}, func() bool {
			return len(cdResNames(t, c, "host")) == 0
		}},
		{"resource field remove", []string{"resource", "field", "remove", "host", "extra"}, func() bool {
			return !strings.Contains(c.Lore("resource", "kind", "show", "host").OK(t).Stdout, "extra")
		}},
	} {
		before := cdTreeHash(t, c)
		e := cdRefused(t, c.Lore(append(tc.args, "--json")...), "E_NEEDS_CONFIRM")
		if !strings.Contains(e.Hint, "--yes") {
			t.Errorf("%s: the hint names no --yes: %+v", tc.name, e)
		}
		if cdTreeHash(t, c) != before || tc.gone() {
			t.Fatalf("%s: the unconfirmed call changed something", tc.name)
		}
		c.MustLore(append(tc.args, "--yes")...)
		if !tc.gone() {
			t.Errorf("%s --yes: nothing deleted", tc.name)
		}
	}
	// a bad or missing id is reported before any question
	cdRefused(t, c.Lore("secrets", "allow", "rm", "bogus", "--json"), "E_INVALID_ID")
	cdRefused(t, c.Lore("secrets", "allow", "rm", "sal_01a1b80ad53375b1ba6cc6a5893e9239", "--json"), "E_NOT_FOUND")
	cdRefused(t, c.Lore("vars", "rm", "nope", "--json"), "E_NOT_FOUND")
	cdRefused(t, c.Lore("resource", "field", "remove", "host", "nope", "--json"), "E_NOT_FOUND")
}

// TestCampaignDataFollowups_ResourceSizeNamesHistory: resources.max_bytes
// bounds the whole row file, so the change history kept in the row counts
// with the current values (decided: the history cap is by count, 100
// changes of up to two values each, so leaving it out would let a row
// grow far past the limit). The refusal says so: how many bytes are
// values and how many history, and the hint names the policy to raise.
// Raising it lets the same edit through.
func TestCampaignDataFollowups_ResourceSizeNamesHistory(t *testing.T) {
	c := resRepo(t)
	c.MustLore("resource", "kind", "add", "host")
	c.MustLore("resource", "field", "add", "host", "note", "--type", "text")
	c.MustLore("resource", "add", "host", "big", "--set", "note="+strings.Repeat("a", 6000))
	edit := []string{"resource", "edit", "host/big", "--set", "note=" + strings.Repeat("b", 6000)}
	before := cdTreeHash(t, c)
	e := cdRefused(t, c.Lore(append(edit, "--json")...), "E_RESOURCE_INVALID")
	for _, want := range []string{"bytes of values", "bytes of the change history", "the limit is 16384"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("refusal does not say %q: %s", want, e.Message)
		}
	}
	if !strings.Contains(e.Hint, "lore policy set --key resources.max_bytes") {
		t.Errorf("hint names no way out: %s", e.Hint)
	}
	if cdTreeHash(t, c) != before {
		t.Fatal("the refused edit wrote something")
	}
	// a value alone over the limit says nothing it does not mean: values
	// are named, and an add carries the history of its first values too
	e = cdRefused(t, c.Lore("resource", "add", "host", "huge", "--set", "note="+strings.Repeat("c", 17000), "--json"), "E_RESOURCE_INVALID")
	if !strings.Contains(e.Message, "bytes of values") {
		t.Errorf("add refusal: %s", e.Message)
	}
	c.MustLore("policy", "set", "resources.max_bytes", "65536", "--yes")
	c.MustLore(edit...)
	if got := resShow(t, c, "host/big"); got.Values["note"] != strings.Repeat("b", 6000) {
		t.Error("the edit under the raised limit did not land")
	}
}

// TestCampaignDataFollowups_FlagIDNotFoundHint: an id given by flag that
// does not exist gets the same answer as a positional one: E_NOT_FOUND
// with the `lore sync peek` hint naming the id's own table (it may have
// landed on main while this branch is older), whatever the command's noun.
func TestCampaignDataFollowups_FlagIDNotFoundHint(t *testing.T) {
	c := resRepo(t)
	const missing = "tsk_01a1b80ad53375b1ba6cc6a5893e9239"
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"positional", []string{"task", "show", missing}},
		{"--parent", []string{"task", "add", "a child", "--parent", missing}},
		{"--on-id", []string{"comment", "add", "--on-table", "tasks", "--on-id", missing, "--body", "hi"}},
		{"--entity", []string{"link", "add", "--entity", missing, "--commit", "HEAD"}},
	} {
		e := cdRefused(t, c.Lore(append(tc.args, "--json")...), "E_NOT_FOUND")
		if !strings.Contains(e.Hint, "lore sync peek") || !strings.Contains(e.Hint, " tasks`") {
			t.Errorf("%s: no peek hint naming tasks: %+v", tc.name, e)
		}
	}
}

// TestCampaignDataFollowups_KindToMeRefused: a kind in the personal store
// could serve no resource (resources never live there, and a repo
// resource cannot name a personal row), and nothing documents personal
// kinds as adoptable, so `resource kind add --to me` is E_INVALID_INPUT
// with the hint to drop --to me, writing nothing; the same kind in the
// repo is fine.
func TestCampaignDataFollowups_KindToMeRefused(t *testing.T) {
	c := resRepo(t)
	before := cdTreeHash(t, c)
	e := cdRefused(t, c.Lore("resource", "kind", "add", "laptop", "--to", "me", "--json"), "E_INVALID_INPUT")
	if e.Hint != "kinds live in the repo (or an org store) so resources can use them: drop --to me" {
		t.Errorf("hint: %q", e.Hint)
	}
	if cdTreeHash(t, c) != before || strings.Contains(c.Lore("resource", "kinds").OK(t).Stdout, "laptop") {
		t.Fatal("the refused kind add wrote something")
	}
	c.MustLore("resource", "kind", "add", "laptop")
	c.MustLore("resource", "add", "laptop", "mine")
}

// TestCampaignDataFollowups_OneIsSingular: with one row, reference or
// resource, the texts of resource import, repoint, field remove, bulk
// delete, secrets show and batch list say "1 row" / "1 reference" /
// "1 host resource", never "1 rows"; two stay plural.
func TestCampaignDataFollowups_OneIsSingular(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "field", "add", "host", "remark", "--type", "text")
	never := []string{"1 rows", "1 references", "1 resources", "1 errors", "value(s)"}
	f := resWriteFile(t, c, "one.yaml", "resources:\n  - kind: host\n    name: h1\n    fields:\n      provider: aws\n      remark: see ${secret:env://LORE_TEST_NOBODY}\n")
	c.MustLore("resource", "import", f, "--yes").Says(t, "imported 1 row (1 created, 0 updated)").Never(t, never...)
	c.MustLore("resource", "add", "host", "h2", "--set", "provider=aws")
	c.MustLore("resource", "add", "service", "api", "--set", "runs_on=h1")
	c.MustLore("resource", "repoint", "h1", "h2", "--yes").Says(t, "repointed 1 row from h1 to h2").Never(t, never...)
	c.MustLore("secrets", "show", "env://LORE_TEST_NOBODY").Says(t, "1 reference in 1 row.").Never(t, never...)
	c.MustLore("batch", "list").Says(t, " 1 row (").Never(t, never...)
	c.MustLore("resource", "field", "remove", "host", "remark", "--drop", "--yes").Says(t, "values dropped from 1 row").Never(t, never...)
	c.MustLore("resource", "add", "host", "h3", "--set", "provider=hetzner")
	c.MustLore("resource", "delete", "--kind", "host", "--where", "provider=aws", "--dry-run").Says(t, "would delete 2 host resources")
	c.MustLore("resource", "delete", "--kind", "host", "--where", "provider=hetzner", "--yes").Says(t, "1 host resource moved").Never(t, never...)
}
