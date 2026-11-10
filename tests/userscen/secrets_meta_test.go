//go:build userscen

package userscen

// Metadata through the real binary (`lore meta --help`): define keys
// (types, strict, closed sets, sensitive), set and refuse values with the
// exact code and next step, reserved names, get/show with sensitive values
// masked unless --show-sensitive (which asks first), rename (a label
// change; the id stays), retype with conversion, history from git with
// sensitive values masked, unset, delete, and secrets in metadata values
// (the question, --secret-ref, never the value in output or files).

import (
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// secMetaGet is `lore meta get --json`.
type secMetaGet struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
	Data  []struct {
		Key       string `json:"key"`
		KeyID     string `json:"key_id"`
		Scope     string `json:"scope"`
		Value     string `json:"value"`
		Sensitive bool   `json:"sensitive"`
	} `json:"data"`
	Masked []string `json:"meta_masked"`
}

// value of key in g ("" when absent).
func (g secMetaGet) value(key string) (string, bool) {
	for _, d := range g.Data {
		if d.Key == key {
			return d.Value, true
		}
	}
	return "", false
}

func TestSecretsMetaKeysAndValues(t *testing.T) {
	s := newSecSolo(t)
	task := secID(t, s.c.Lore("task", "add", "ship it", "--json"))
	s.c.MustLore("meta", "add", "cost_hours", "--type", "number", "--strict").Says(t, "cost_hours (number, strict)")
	s.c.MustLore("meta", "add", "client_tier", "--type", "text", "--strict", "--values", "gold,silver").Says(t, "values gold, silver")
	s.c.MustLore("meta", "add", "pager_pin", "--type", "text", "--sensitive").Says(t, "pager_pin (text, sensitive)")
	s.c.Lore("meta", "add", "cost_hours", "--type", "number").Refused(t)
	s.c.Lore("meta", "add", "Bad-Key", "--type", "text").Fails(t, 20)
	s.c.Lore("meta", "add", "no_type").Fails(t, 20, "E_INVALID_INPUT")

	cases := []struct {
		pair string
		code int
		says []string
	}{
		{"cost_hours=3", 0, nil},
		{"cost_hours=abc", 20, []string{"E_META_TYPE", `"abc" is not a number`, "plain digits"}},
		{"client_tier=gold", 0, nil},
		{"client_tier=bronze", 20, []string{"E_META_VALUE", "allowed: gold, silver", "lore meta value add client_tier <value>"}},
		{"pager_pin=4711", 0, nil},
		{"note=x", 20, []string{"E_META_RESERVED", "is a column of a lore table", "use another name"}},
		{"title=x", 20, []string{"E_META_RESERVED"}},
		{"city=東京", 0, nil},
	}
	for _, c := range cases {
		r := s.c.Lore("meta", "set", task, c.pair)
		if c.code == 0 {
			r.OK(t)
			continue
		}
		r.Fails(t, c.code, c.says...)
	}
	// A pair without a row id names the problem.
	s.c.Lore("meta", "set", "cost_hours=3").Fails(t, 20, "E_INVALID_ID", "is not a row id")
	s.c.Lore("meta", "set", "tsk_00000000000000000000000000000000", "cost_hours=3").Refused(t)

	g := th.DecodeJSON[secMetaGet](t, s.c.Lore("meta", "get", task, "--json"))
	if v, _ := g.value("cost_hours"); v != "3" {
		t.Fatalf("cost_hours: %+v", g)
	}
	if v, _ := g.value("pager_pin"); v != "•••" || len(g.Masked) != 1 || g.Masked[0] != "pager_pin" {
		t.Fatalf("a sensitive value is masked in --json: %+v", g)
	}
	if v, _ := g.value("city"); v != "東京" {
		t.Fatalf("unicode value: %+v", g)
	}
	s.c.MustLore("meta", "get", task).Says(t, "pager_pin\t•••").Never(t, "4711")
	s.c.MustLore("meta", "show", task).Never(t, "4711")
	s.c.MustLore("task", "show", task).Never(t, "4711")
	s.c.MustLore("task", "list", "--json").Never(t, "4711")
	s.c.Lore("meta", "get", task, "--show-sensitive").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "--yes")
	s.c.MustLore("meta", "get", task, "--show-sensitive", "--yes").Says(t, "pager_pin\t4711")
	s.c.MustLore("meta", "get", task, "client_tier").Says(t, "gold").Never(t, "cost_hours")

	// unset, twice
	s.c.MustLore("meta", "unset", task, "city")
	s.c.MustLore("meta", "get", task).Never(t, "city")
	if r := s.c.Lore("meta", "unset", task, "city"); r.Code != 0 && r.Code != 20 && r.Code != th.ExitNotFound {
		t.Fatalf("a second unset is a no-op or a plain refusal: %s", r)
	}
	s.c.MustLore("meta", "get", task).Never(t, "city")
}

// rename keeps the key id; retype converts the stored values; history from
// git masks sensitive values; delete removes an unused key.
func TestSecretsMetaRenameRetypeHistory(t *testing.T) {
	s := newSecSolo(t)
	task := secID(t, s.c.Lore("task", "add", "ship it", "--json"))
	s.c.MustLore("meta", "add", "cost_hours", "--type", "number")
	s.c.MustLore("meta", "add", "pager_pin", "--type", "text", "--sensitive")
	s.c.MustLore("meta", "set", task, "cost_hours=3")
	s.c.MustLore("meta", "set", task, "pager_pin=4711")
	before := th.DecodeJSON[secMetaGet](t, s.c.Lore("meta", "get", task, "--json"))
	s.c.CommitAll("first values")

	s.c.MustLore("meta", "rename", "cost_hours", "effort_hours").Says(t, "renamed cost_hours → effort_hours", "no values changed")
	s.c.Lore("meta", "rename", "nosuch_key", "other").Refused(t)
	s.c.MustLore("meta", "keys").Says(t, "effort_hours").Never(t, "cost_hours")
	s.c.MustLore("meta", "rename", "effort_hours", "cost_hours", "--rewrite-rows")
	after := th.DecodeJSON[secMetaGet](t, s.c.Lore("meta", "get", task, "--json"))
	var idBefore, idAfter string
	for _, d := range before.Data {
		if d.Key == "cost_hours" {
			idBefore = d.KeyID
		}
	}
	for _, d := range after.Data {
		if d.Key == "cost_hours" {
			idAfter = d.KeyID
		}
	}
	if idBefore == "" || idBefore != idAfter {
		t.Fatalf("rename keeps the key id: %q -> %q", idBefore, idAfter)
	}

	// retype: dry run writes nothing, then convert
	s.c.MustLore("meta", "edit", "cost_hours", "--type", "text", "--dry-run").Says(t, "number → text")
	if g := th.DecodeJSON[secMetaGet](t, s.c.Lore("meta", "get", task, "--json")); g.Data[0].Value != "3" {
		t.Fatalf("dry run: %+v", g)
	}
	s.c.Lore("meta", "edit", "cost_hours", "--type", "text").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM")
	s.c.MustLore("meta", "edit", "cost_hours", "--type", "text", "--yes").Says(t, "1 value converted")
	s.c.MustLore("meta", "set", task, "cost_hours=about three")
	s.c.Lore("meta", "edit", "cost_hours", "--type", "number", "--yes").Fails(t, 20, "E_META_TYPE", "1 value does not fit number, nothing written", task, "--keep-as-text")
	s.c.CommitAll("second values")

	h := th.DecodeJSON[struct {
		Count int `json:"count"`
		Lines []struct {
			Key    string `json:"key"`
			Change string `json:"change"`
			To     string `json:"to"`
		} `json:"lines"`
	}](t, s.c.Lore("meta", "history", task, "--json"))
	if h.Count == 0 {
		t.Fatal("history is empty")
	}
	sawPin := false
	for _, l := range h.Lines {
		if l.Key == "pager_pin" {
			sawPin = true
			if l.To != "•••" {
				t.Fatalf("history masks a sensitive value: %+v", l)
			}
		}
	}
	if !sawPin {
		t.Fatalf("history names the sensitive key: %+v", h)
	}
	s.c.MustLore("meta", "history", task).Never(t, "4711")
	s.c.MustLore("meta", "history", task, "pager_pin").Says(t, "set •••").Never(t, "4711", "cost_hours")

	s.c.MustLore("meta", "add", "unused_key", "--type", "text")
	s.c.MustLore("meta", "delete", "unused_key", "--yes")
	s.c.MustLore("meta", "keys").Never(t, "unused_key")
}

// Secrets in metadata: the question names meta.<key>; --secret-ref stores
// a reference in a text key (the reference listed by secrets list); a
// typed (number) key cannot hold a reference; the value is in no file.
func TestSecretsMetaSecretValues(t *testing.T) {
	s := newSecSolo(t)
	key := secAWSKey()
	task := secID(t, s.c.Lore("task", "add", "vendor access", "--json"))
	s.c.Lore("meta", "set", task, "vendor_key="+key).
		Fails(t, th.ExitNeedsConfirm, "meta.vendor_key looks like a secret (aws_access_key)").Never(t, key)
	s.c.Lore("meta", "set", task, "vendor_key="+key, "--json").Never(t, key)
	s.c.MustLore("meta", "set", task, "vendor_key="+key, "--secret-ref", "gopass://acme/vendor").
		Says(t, "vendor_key = ${secret:gopass://acme/vendor}").Never(t, key)
	s.c.MustLore("meta", "get", task, "vendor_key").Says(t, "${secret:gopass://acme/vendor}").Never(t, key)
	s.c.MustLore("secrets", "list").Says(t, "gopass://acme/vendor", task)
	s.c.MustLore("meta", "set", task, "other_key=x "+key, "--secret-unstored").Never(t, key)
	s.c.MustLore("meta", "get", task, "other_key").Says(t, "x ${secret:unstored}")
	if hit := secDataHolding(t, s.c, key); len(hit) != 0 {
		t.Fatalf("metadata secret reached .lore/data: %v", hit)
	}
	s.c.MustLore("meta", "history", task).Never(t, key)
}

// A key=value pair given without the row id is refused E_INVALID_ID
// naming the key and the right form, never echoing the value: the value
// never went through the write scan, so a pasted secret would otherwise
// be printed (and logged by agents) in clear.
func TestSecretsMetaMissingIDNeverEchoesValue(t *testing.T) {
	s := newSecSolo(t)
	for _, f := range secFixtures()[:4] {
		r := s.c.Lore("meta", "set", "vendor_key="+f.value)
		r.Fails(t, 20, "E_INVALID_ID", `"vendor_key=…" is not a row id`, "lore meta set <id> vendor_key=<value>").Never(t, f.value, secTail(f.value))
		s.c.Lore("meta", "set", "vendor_key="+f.value, "--json").Never(t, f.value, secTail(f.value))
	}
	// A plain bad id is still named as typed.
	s.c.Lore("meta", "get", "not-an-id").Fails(t, 20, "E_INVALID_ID", `"not-an-id" is not a row id`)
}
