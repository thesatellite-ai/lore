//go:build userscen

package userscen

// Project variables through the real binary (`lore vars --help`: "Variables
// are data … Variables never hold secrets"): set adds then changes, show,
// list and where with their --json shapes, every --type validated, names
// must be snake_case, references ${var:…} resolve with --expand in any
// text field, a dangling reference needs --allow-dangling, a loop of any
// length is refused with its chain and nothing written, rename keeps the
// old name as an alias, rm leaves a tombstone, a missing store is refused
// with the next step, and a secret-looking value meets the secret question.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// secVar is one variable as `lore vars show|list --json` prints it.
type secVar struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	Store string `json:"store"`
}

// Lifecycle: set → show → change → list → rename (alias) → rm (twice).
func TestSecretsVarsLifecycle(t *testing.T) {
	s := newSecSolo(t)
	s.c.MustLore("vars", "set", "db_host", "db.example.com", "--type", "dns").
		Says(t, "Added var_", "db_host = db.example.com (dns) in repo", "not shareable: clients see ${var:<name>}")
	one := th.DecodeJSON[struct{ Data secVar }](t, s.c.Lore("vars", "show", "db_host", "--json")).Data
	if one.Name != "db_host" || one.Value != "db.example.com" || one.Type != "dns" || one.Store != "repo" || !strings.HasPrefix(one.ID, "var_") {
		t.Fatalf("show --json: %+v", one)
	}
	s.c.MustLore("vars", "set", "db_host", "db2.example.com").Says(t, "Changed var_", "db2.example.com (dns)")
	again := th.DecodeJSON[struct{ Data secVar }](t, s.c.Lore("vars", "show", "db_host", "--json")).Data
	if again.ID != one.ID || again.Type != "dns" || again.Value != "db2.example.com" {
		t.Fatalf("a change keeps the id and the type: %+v vs %+v", again, one)
	}
	// Setting the same value twice is not an error and keeps one row.
	s.c.MustLore("vars", "set", "db_host", "db2.example.com")
	l := th.DecodeJSON[struct {
		Count int      `json:"count"`
		Data  []secVar `json:"data"`
	}](t, s.c.Lore("vars", "list", "--json"))
	if l.Count != 1 || len(l.Data) != 1 || l.Data[0].ID != one.ID {
		t.Fatalf("list after three sets: %+v", l)
	}
	s.c.MustLore("vars", "where", "db_host").Says(t, "db_host", "repo")

	s.c.MustLore("vars", "rename", "db_host", "database_host").Says(t, "renamed variable db_host → database_host", "old name kept as an alias")
	s.c.MustLore("vars", "show", "db_host").Says(t, "database_host = db2.example.com", "aliases: db_host")
	m := secID(t, s.c.Lore("memory", "add", "--body", "connect to ${var:db_host}", "--json"))
	s.c.MustLore("memory", "show", m, "--expand").Says(t, "connect to db2.example.com")

	s.c.MustLore("vars", "rm", "database_host", "--yes").Says(t, "removed variable database_host")
	// The removed variable is in the trash, and show names the way back.
	s.c.Lore("vars", "show", "database_host").Fails(t, th.ExitRefused, "E_IN_TRASH", "lore trash restore "+one.ID)
	s.c.Lore("vars", "rm", "database_host").Refused(t)
	s.c.MustLore("vars", "list").Never(t, "database_host")
	// A removed variable's name may be set again.
	s.c.MustLore("vars", "set", "database_host", "db3.example.com").Says(t, "Added")
}

// Names and types: the input space of `vars set` (name shape × --type ×
// value), each refusal with its code and nothing written.
func TestSecretsVarsNameAndTypeMatrix(t *testing.T) {
	s := newSecSolo(t)
	cases := []struct {
		name, value, typ string
		ok               bool
		says             string
	}{
		{"port", "5432", "number", true, ""},
		{"port_bad", "abc", "number", false, `"abc" is not a number`},
		{"site", "https://example.com/x", "url", true, ""},
		{"site_bad", "not a url", "url", false, "E_INVALID_INPUT"},
		{"host_dns", "db.example.com", "dns", true, ""},
		{"host_ip", "10.0.0.1", "ip", true, ""},
		{"host_ip_bad", "10.0.0.999", "ip", false, "E_INVALID_INPUT"},
		{"net", "10.0.0.0/8", "cidr", true, ""},
		{"net_bad", "10.0.0.0/99", "cidr", false, "E_INVALID_INPUT"},
		{"owner", "person@example.com", "email", true, ""},
		{"owner_bad", "not-an-email", "email", false, "E_INVALID_INPUT"},
		{"launch", "2026-11-07", "date", true, ""},
		{"launch_bad", "07/11/2026", "date", false, "E_INVALID_INPUT"},
		{"timeout", "1h30m0s", "duration", true, ""},
		{"timeout_bad", "ninety", "duration", false, "E_INVALID_INPUT"},
		{"vault_ref", "gopass://acme/hub/token", "secret_ref", true, ""},
		{"kind_bad", "x", "colour", false, "E_INVALID_INPUT"},
		{"unicode_value", "サーバー東京", "text", true, ""},
		{"long_value", strings.Repeat("v", 4000), "text", true, ""},
		{"Bad-Name", "x", "text", false, `variable name "Bad-Name" is not snake_case`},
		{"with space", "x", "text", false, "is not snake_case"},
		{"émoji", "x", "text", false, "is not snake_case"},
		{"9lives", "x", "text", false, "is not snake_case"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := s.c.Lore("vars", "set", c.name, c.value, "--type", c.typ)
			if c.ok {
				r.OK(t)
				got := th.DecodeJSON[struct{ Data secVar }](t, s.c.Lore("vars", "show", c.name, "--json")).Data
				if got.Value != c.value || got.Type != c.typ {
					t.Fatalf("stored %+v, want %s (%s)", got, c.value, c.typ)
				}
				return
			}
			r.Fails(t, 20, c.says)
			s.c.MustLore("vars", "list").Never(t, c.name+"\t")
		})
	}
	// The refusal suggests the snake_case form (INTEG-QUESTIONS-10).
	s.c.Lore("vars", "set", "Bad-Name", "x").Fails(t, 20, "is not snake_case", "use a snake_case name such as bad_name")
	// An empty name and a missing value are refused.
	s.c.Lore("vars", "set").Fails(t, 20, "E_INVALID_INPUT")
	s.c.Lore("vars", "set", "", "x").Fails(t, 20, "E_INVALID_INPUT")
}

// References: a dangling one is refused with the next step unless
// --allow-dangling; loops of one, two and three variables are refused with
// the chain and write nothing; a chain and a diamond resolve with --expand
// in a memory, and a variable may hold a secret reference without its value.
func TestSecretsVarsReferencesAndCycles(t *testing.T) {
	s := newSecSolo(t)
	s.c.Lore("vars", "set", "a", "${var:b}").
		Fails(t, 20, "E_REF_DANGLING", "a refers to ${var:b}", "lore vars set b <value> first, or pass --allow-dangling")
	s.c.MustLore("vars", "set", "a", "${var:b}", "--allow-dangling")
	s.c.Lore("vars", "set", "b", "${var:a}").
		Fails(t, 20, "E_REF_CYCLE", "var:b -> var:a -> var:b", "nothing written")
	s.c.Lore("vars", "show", "b").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	s.c.Lore("vars", "set", "p", "x ${var:p}").Fails(t, 20, "E_REF_CYCLE", "var:p -> var:p")
	s.c.Lore("vars", "set", "p", "${var:p}", "--allow-dangling").Fails(t, 20, "E_REF_CYCLE")
	s.c.MustLore("vars", "set", "x", "${var:y}", "--allow-dangling")
	s.c.MustLore("vars", "set", "y", "${var:z}", "--allow-dangling")
	s.c.Lore("vars", "set", "z", "${var:x}").Fails(t, 20, "E_REF_CYCLE", "var:z -> var:x -> var:y -> var:z")
	// Changing an existing variable into a loop is refused and keeps its old value.
	s.c.MustLore("vars", "set", "z", "end")
	s.c.Lore("vars", "set", "z", "${var:x}").Fails(t, 20, "E_REF_CYCLE")
	s.c.MustLore("vars", "show", "z").Says(t, "z = end")

	// chain + diamond resolve
	s.c.MustLore("vars", "set", "region", "eu-west")
	s.c.MustLore("vars", "set", "host", "api.${var:region}.example.com")
	s.c.MustLore("vars", "set", "url", "https://${var:host}/${var:region}", "--type", "url")
	m := secID(t, s.c.Lore("memory", "add", "--body", "call ${var:url} from ${var:region}", "--json"))
	type expanded struct {
		Expanded map[string]string `json:"expanded"`
		Refs     []struct {
			Ref   string `json:"ref"`
			Value string `json:"value"`
			State string `json:"state"`
		} `json:"refs"`
	}
	e := th.DecodeJSON[expanded](t, s.c.Lore("memory", "show", m, "--expand", "--json"))
	if e.Expanded["body"] != "call https://api.eu-west.example.com/eu-west from eu-west" {
		t.Fatalf("expanded body: %+v", e)
	}
	for _, r := range e.Refs {
		if r.State != "expanded" {
			t.Fatalf("every reference expands: %+v", e.Refs)
		}
	}
	// Without --expand the row keeps the references as written.
	s.c.MustLore("memory", "show", m).Says(t, "call ${var:url} from ${var:region}")

	// A variable referencing a secret keeps the reference; --expand masks it.
	s.c.MustLore("vars", "set", "api_token", "${secret:gopass://acme/hub/token}")
	m2 := secID(t, s.c.Lore("memory", "add", "--body", "token ${var:api_token}", "--json"))
	s.c.MustLore("memory", "show", m2, "--expand").Says(t, "••••••").Never(t, "gopass://acme/hub/token\n")
}

// A secret-looking value meets the secret question (vars never hold
// secrets); --secret-ref stores a reference; --to me without a personal
// store and --to an unknown store are refused with the next step.
func TestSecretsVarsSecretsAndPlaces(t *testing.T) {
	s := newSecSolo(t)
	key := secAWSKey()
	s.c.Lore("vars", "set", "deploy_key", key).
		Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "value looks like a secret (aws_access_key)").Never(t, key)
	s.c.Lore("vars", "set", "deploy_key", key, "--json").Never(t, key)
	// Both redacting answers the question offers work on a variable (the
	// value is a json column; before the fix both failed E_SECRET_DETECTED).
	s.c.MustLore("vars", "set", "deploy_key", key, "--secret-ref", "gopass://acme/deploy").
		Says(t, "deploy_key = ${secret:gopass://acme/deploy}").Never(t, key)
	s.c.MustLore("vars", "show", "deploy_key").Says(t, "${secret:gopass://acme/deploy}").Never(t, key)
	s.c.MustLore("secrets", "list").Says(t, "gopass://acme/deploy")
	s.c.MustLore("vars", "set", "other_key", "prefix "+key+" suffix", "--secret-unstored").Never(t, key)
	s.c.MustLore("vars", "show", "other_key").Says(t, "prefix ${secret:unstored} suffix").Never(t, key)
	s.c.MustLore("task", "list").Says(t, "Store the secret redacted from")
	// Changing an existing variable to a secret asks again.
	s.c.Lore("vars", "set", "deploy_key", key).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM").Never(t, key)
	if hit := secDataHolding(t, s.c, key); len(hit) != 0 {
		t.Fatalf("the key reached .lore/data: %v", hit)
	}

	s.c.Lore("vars", "set", "region", "eu", "--to", "me").
		Fails(t, th.ExitNotFound, "E_NOT_FOUND", "no store named me", "lore store init me")
	s.c.Lore("vars", "set", "region", "eu", "--to", "nosuchstore").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "nosuchstore")
	s.c.MustLore("vars", "list").Never(t, "region")
}
