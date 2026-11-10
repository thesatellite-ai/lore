//go:build userscen

package userscen

// campaign_data_secrets_test.go — `lore vars …` and `lore secrets …` with
// fake vault references only: a fake `gopass` first on PATH answers the
// one entry the test stores and logs every call, so no real vault is ever
// opened. Covers a variable's life (set → show → where → rename → rm →
// trash restore), ${var:…} in every kind of text field, secret references
// in a body and in a metadata value, the bulk rewrites (rename-entry,
// move, relabel, link) with their refusals, rules and allow entries.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// cdFakeVault returns c with a fake gopass first on PATH: `gopass show -o
// acme/db/password` prints a placeholder value, any other entry fails as
// gopass does; every call is appended to the returned log file.
func cdFakeVault(t *testing.T, c *th.Clone) (*th.Clone, string) {
	t.Helper()
	bin := filepath.Join(c.A.Home, "fakebin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(c.A.Home, "fakevault.log")
	script := "#!/bin/sh\necho \"$@\" >> '" + log + "'\n" +
		"if [ \"$1 $2 $3\" = \"show -o acme/db/password\" ]; then echo placeholder-value; exit 0; fi\n" +
		"echo \"Error: entry is not in the password store\" >&2; exit 11\n"
	if err := os.WriteFile(filepath.Join(bin, "gopass"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	path := ""
	for _, kv := range c.Command("version").Env {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = p
		}
	}
	return c.With("PATH=" + bin + string(os.PathListSeparator) + path), log
}

// TestCampaignData_VarsLifecycle: set → set again → show → list → where →
// rename (alias kept) → rm → rm again (in the trash) → trash restore.
func TestCampaignData_VarsLifecycle(t *testing.T) {
	c := resRepo(t)
	id := cdIDOf(t, c.Lore("vars", "set", "db_host", "db.acme.example", "--json"))
	c.MustLore("vars", "set", "db_host", "db.acme.example").Says(t, "db_host = db.acme.example")
	c.MustLore("vars", "set", "db_host", "db2.acme.example")
	if s := c.MustLore("vars", "show", "db_host").Stdout; !strings.Contains(s, "db2.acme.example") {
		t.Fatalf("vars show: %s", s)
	}
	if l := c.MustLore("vars", "list", "--json").Stdout; strings.Count(l, `"db_host"`) != 1 {
		t.Errorf("vars list holds db_host %d times: %s", strings.Count(l, `"db_host"`), l)
	}
	c.MustLore("vars", "where", "db_host").Says(t, "repo")
	c.MustLore("vars", "rename", "db_host", "database_host").Says(t, "alias")
	c.MustLore("vars", "show", "db_host").Says(t, "database_host") // the alias still answers
	c.MustLore("vars", "rm", "database_host", "--yes")
	cdRefused(t, c.Lore("vars", "show", "database_host", "--json"), "E_IN_TRASH")
	cdRefused(t, c.Lore("vars", "rm", "database_host", "--json"), "E_IN_TRASH")
	c.MustLore("trash", "restore", id, "--yes")
	c.MustLore("vars", "show", "database_host").Says(t, "db2.acme.example")
}

// cdVarValue is the value `vars show <name> --json` prints.
func cdVarValue(t *testing.T, c *th.Clone, name string) string {
	t.Helper()
	return th.DecodeJSON[struct {
		Data struct {
			Value string `json:"value"`
		} `json:"data"`
	}](t, c.Lore("vars", "show", name, "--json")).Data.Value
}

// TestCampaignData_VarsOddValues: values of every shape round-trip; bad
// names and values are refused with their codes and nothing is written.
func TestCampaignData_VarsOddValues(t *testing.T) {
	c := resRepo(t)
	for i, v := range []string{"", "🚀 שלום café", cdLong(), "select * from x", "a b/c"} {
		name := "odd_" + string(rune('a'+i))
		c.MustLore("vars", "set", name, v)
		if got := cdVarValue(t, c, name); got != v {
			t.Errorf("var %s: value %.30q not shown back", name, v)
		}
	}
	before := cdTreeHash(t, c)
	cdRefused(t, c.Lore("vars", "set", "bad name", "x", "--json"), "E_INVALID_INPUT")
	cdRefused(t, c.Lore("vars", "set", "Upper", "x", "--json"), "E_INVALID_INPUT")
	cdRefused(t, c.Lore("vars", "show", "no_such_var", "--json"), "E_NOT_FOUND")
	cdRefused(t, c.Lore("vars", "rename", "no_such_var", "other", "--json"), "E_NOT_FOUND")
	if cdTreeHash(t, c) != before {
		t.Error("a refused vars command changed the tree")
	}
}

// TestCampaignData_VarRefsInEveryTextField: ${var:api_host} written in a
// memory body, a decision body, a rule, a task title and a comment body is
// filled in by `lore show --expand`, which names the variable it used; the
// stored text is never rewritten.
func TestCampaignData_VarRefsInEveryTextField(t *testing.T) {
	c := resRepo(t)
	c.MustLore("vars", "set", "api_host", "api.acme.example")
	ref := "${var:api_host}"
	m := cdIDOf(t, c.Lore("memory", "add", "--body", "call "+ref+" first", "--json"))
	rows := map[string]string{
		"memory":   m,
		"decision": cdIDOf(t, c.Lore("decision", "add", "--title", "use "+ref, "--body", "because "+ref+" is closer", "--json")),
		"rule":     cdIDOf(t, c.Lore("rule", "add", "always call "+ref+" over TLS", "--severity", "must", "--json")),
		"task":     cdIDOf(t, c.Lore("task", "add", "move "+ref, "--to", "repo", "--commitment", "accepted", "--json")),
		"comment":  cdIDOf(t, c.Lore("comment", "add", "--on-table=memories", "--on-id="+m, "--body=ping "+ref, "--json")),
	}
	for kind, id := range rows {
		r := c.Lore("show", id, "--expand")
		if r.Code != 0 || !strings.Contains(r.Stdout, "api.acme.example") {
			t.Errorf("%s %s: --expand does not fill the variable: %s", kind, id, r)
		}
		if plain := c.MustLore("show", id).Stdout; !strings.Contains(plain, ref) {
			t.Errorf("%s %s: the stored text lost the reference: %.300s", kind, id, plain)
		}
	}
	// a changed value reaches every row; a removed variable shows dangling
	c.MustLore("vars", "set", "api_host", "api2.acme.example")
	if r := c.MustLore("show", m, "--expand"); !strings.Contains(r.Stdout, "api2.acme.example") {
		t.Errorf("new value not expanded: %s", r.Stdout)
	}
	c.MustLore("vars", "rm", "api_host", "--yes")
	if r := c.MustLore("show", m, "--expand"); !strings.Contains(r.Stdout, "dangling") {
		t.Errorf("a removed variable is not shown as dangling: %s", r.Stdout)
	}
}

// TestCampaignData_SecretRefs: references in a body and in a metadata
// value are listed (no vault opened), shown by ref and by row, renamed in
// one batch (an entry name the syntax refuses is refused before any row is
// rewritten), and checked against the fake vault only.
func TestCampaignData_SecretRefs(t *testing.T) {
	c := resRepo(t)
	m := cdIDOf(t, c.Lore("memory", "add", "--body", "db pw is ${secret:gopass://acme/db/password}", "--json"))
	task := cdIDOf(t, c.Lore("task", "add", "rotate", "--to", "repo", "--commitment", "accepted", "--json"))
	c.MustLore("meta", "set", task, "api_key=${secret:gopass://acme/api/key}")
	list := func() string { return c.MustLore("secrets", "list", "--json").Stdout }
	l := list()
	for _, want := range []string{"gopass://acme/db/password", "gopass://acme/api/key", m, task} {
		if !strings.Contains(l, want) {
			t.Errorf("secrets list misses %s: %s", want, l)
		}
	}
	c.MustLore("secrets", "show", "gopass://acme/db/password").Says(t, "Not revealed")
	c.MustLore("secrets", "show", m).Says(t, "acme/db/password")
	cdRefused(t, c.Lore("secrets", "show", cdMemory(t, c, "no refs here"), "--json"), "E_NOT_FOUND")

	before := cdTreeHash(t, c)
	for _, bad := range []string{"a b/x", "acme db"} {
		e := cdRefused(t, c.Lore("secrets", "rename-entry", "--manager", "gopass", "--from", "acme/db/password", "--to", bad, "--json"), "E_REF_SYNTAX")
		if !strings.Contains(e.Message, bad) {
			t.Errorf("rename-entry --to %q: %q", bad, e.Message)
		}
	}
	cdRefused(t, c.Lore("secrets", "rename-entry", "--manager", "nosuch", "--from", "a", "--to", "b", "--json"), "E_REF_SCHEME")
	cdRefused(t, c.Lore("secrets", "rename-entry", "--manager", "gopass", "--json"), "E_INVALID_INPUT")
	if cdTreeHash(t, c) != before {
		t.Fatal("a refused rename-entry changed the tree")
	}
	if !strings.Contains(list(), "gopass://acme/db/password") {
		t.Fatal("a refused rename-entry lost the reference")
	}
	c.MustLore("secrets", "rename-entry", "--manager", "gopass", "--from", "acme/db/password", "--to", "acme/db/main").Says(t, "Renamed 1")
	c.MustLore("secrets", "rename-entry", "--manager", "gopass", "--from", "acme/db/password", "--to", "acme/db/main").Says(t, "Nothing to change")
	if l := list(); !strings.Contains(l, "gopass://acme/db/main") || strings.Contains(l, "gopass://acme/db/password") {
		t.Errorf("after rename-entry: %s", l)
	}
	c.MustLore("secrets", "rename-entry", "--manager", "gopass", "--from", "acme/db/main", "--to", "acme/db/password")

	// check reads only the fake vault, names the entry it could not read
	fv, log := cdFakeVault(t, c)
	r := fv.Lore("secrets", "check", "--json")
	calls, _ := os.ReadFile(log)
	for _, l := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if l != "" && !strings.HasPrefix(l, "show -o acme/") {
			t.Errorf("secrets check ran gopass %q", l)
		}
	}
	if !strings.Contains(r.Out(), "acme/api/key") {
		t.Errorf("secrets check does not name the unreadable entry: %s", r)
	}
	if strings.Contains(r.Out(), "placeholder-value") {
		t.Errorf("secrets check printed a vault value: %s", r)
	}
}

// TestCampaignData_SecretsBulkRefusals: move to an unknown holder, link on
// a row without a placeholder, rule on/off of an unknown rule and allow rm
// of a bad id are refused with their codes; rules and allow list answer
// their empty state.
func TestCampaignData_SecretsBulkRefusals(t *testing.T) {
	c := resRepo(t)
	m := cdIDOf(t, c.Lore("memory", "add", "--body", "pw ${secret:gopass://acme/db/password?holder=alice}", "--json"))
	before := cdTreeHash(t, c)
	cdRefused(t, c.Lore("secrets", "move", "--from", "alice", "--to", "nobody", "--yes", "--json"), "E_NOT_FOUND")
	cdRefused(t, c.Lore("secrets", "move", "--json"), "E_INVALID_INPUT")
	cdRefused(t, c.Lore("secrets", "link", m, "--at", "1", "--ref", "gopass://acme/db/other", "--json"), "E_NOT_FOUND")
	cdRefused(t, c.Lore("secrets", "allow", "rm", "nosuch", "--yes", "--json"), "E_INVALID_ID")
	if cdTreeHash(t, c) != before {
		t.Error("a refused secrets command changed the tree")
	}
	c.MustLore("secrets", "rules").Says(t, "no rule rows")
	c.MustLore("secrets", "allow", "list").Says(t, "no allow entries")
	c.MustLore("secrets", "relabel").Says(t, "Nothing to change")
	// unstored placeholder → link fills it
	u := cdIDOf(t, c.Lore("memory", "add", "--body", "token ${secret:unstored}", "--json"))
	c.MustLore("secrets", "link", u, "--at", "1", "--ref", "gopass://acme/ci/token")
	if l := c.MustLore("secrets", "list", "--json").Stdout; !strings.Contains(l, "gopass://acme/ci/token") {
		t.Errorf("secrets link did not fill the placeholder: %s", l)
	}
}

// TestCampaignData_SecretRules: add → list → edit → off → on, each step
// listed; a bad regex is refused.
func TestCampaignData_SecretRules(t *testing.T) {
	c := resRepo(t)
	c.MustLore("secrets", "rule", "add", "acme-token", "--regex", "acme_[a-z0-9]{32}", "--yes")
	c.MustLore("secrets", "rules").Says(t, "acme-token")
	c.MustLore("secrets", "rule", "edit", "acme-token", "--description", "the acme API token", "--yes")
	c.MustLore("secrets", "rule", "off", "acme-token", "--reason", "fixtures only", "--yes")
	c.MustLore("secrets", "rules").Says(t, "acme-token")
	c.MustLore("secrets", "rule", "on", "acme-token", "--yes")
	cdRefused(t, c.Lore("secrets", "rule", "add", "broken", "--regex", "([a-z", "--yes", "--json"), "E_INVALID_INPUT")
	// the rule now catches its shape on a write
	tok := "acme_" + strings.Repeat("a1", 16)
	cdRefused(t, c.Lore("memory", "add", "--body", "token "+tok, "--json"), "E_NEEDS_CONFIRM")
	if len(secDataHolding(t, c, tok)) != 0 {
		t.Error("the refused token reached .lore/data")
	}
}
