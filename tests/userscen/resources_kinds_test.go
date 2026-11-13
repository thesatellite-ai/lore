//go:build userscen

// Resource kinds and fields as a person defines them: kind names (valid,
// not snake_case, reserved route names, empty), field names (credential
// look-alikes, not snake_case), every value type with a value that fits and
// one that does not, a closed value set, required fields, number bounds,
// the --json shapes of kinds/kind show, and removing a field that rows
// still use (E_RESOURCE_IN_USE, then --drop).
package userscen

import (
	"slices"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// A kind name is snake_case, starts with a letter, and is not one of the
// resource commands' REST route names; each refusal names its code, exit
// status and what to do next.
func TestResourcesKindNameMatrix(t *testing.T) {
	c := resRepo(t)
	for _, tc := range []struct {
		name, kind string
		ok         bool
		code       string
		say        []string
	}{
		{name: "snake_case", kind: "db_cluster", ok: true},
		{name: "digits after the first letter", kind: "host2", ok: true},
		{name: "a plain English word", kind: "select", ok: true},
		{name: "upper case", kind: "Host", code: "E_RESOURCE_NAME", say: []string{"not snake_case"}},
		{name: "space", kind: "my kind", code: "E_RESOURCE_NAME", say: []string{"not snake_case"}},
		{name: "dash", kind: "x-y", code: "E_RESOURCE_NAME"},
		{name: "leading digit", kind: "1host", code: "E_RESOURCE_NAME"},
		{name: "leading underscore", kind: "_host", code: "E_RESOURCE_NAME"},
		{name: "unicode", kind: "hôst", code: "E_RESOURCE_NAME"},
		{name: "empty", kind: "", code: "E_INVALID_INPUT", say: []string{"missing required field kind"}},
		// RES routes: a kind named like a fixed verb would share its REST path.
		{name: "reserved import", kind: "import", code: "E_INVALID_INPUT", say: []string{"/v1/resources/import", "import_item"}},
		{name: "reserved kinds", kind: "kinds", code: "E_INVALID_INPUT", say: []string{"reserved:"}},
		{name: "reserved export", kind: "export", code: "E_INVALID_INPUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := c.Lore("resource", "kind", "add", tc.kind)
			if tc.ok {
				r.OK(t).Says(t, "added kind knd_", tc.kind)
				return
			}
			r.Fails(t, resExitInvalid, append([]string{tc.code}, tc.say...)...)
		})
	}
	// The same kind twice is refused; the first one stays.
	c.Lore("resource", "kind", "add", "db_cluster").Refused(t, "db_cluster")
	kinds := th.DecodeJSON[kindsJSON](t, c.Lore("resource", "kinds", "--json"))
	var names []string
	for _, k := range kinds.Data {
		names = append(names, k.Name)
	}
	slices.Sort(names)
	if want := []string{"db_cluster", "host2", "select"}; !slices.Equal(names, want) {
		t.Fatalf("kinds after the matrix = %v, want %v (no refused name may be written)", names, want)
	}
}

// kindsJSON is `lore resource kinds --json`.
type kindsJSON struct {
	Count int        `json:"count"`
	Data  []kindJSON `json:"data"`
}

// kindJSON is one kind of `kinds --json` and the data of `kind show --json`.
type kindJSON struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Store       string      `json:"store"`
	Description string      `json:"description"`
	Count       int         `json:"count"`
	Fields      []fieldJSON `json:"fields"`
}

// fieldJSON is one field of a kind.
type fieldJSON struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Values   []string `json:"values"`
	Min      string   `json:"min"`
	Max      string   `json:"max"`
	RefKind  string   `json:"ref_kind"`
}

// kindShowJSON is the kind show envelope.
type kindShowJSON struct {
	Data kindJSON `json:"data"`
}

// Field names: snake_case, and never a credential's name (lore never
// stores a secret; a secret_ref field holds the entry name).
func TestResourcesFieldNameMatrix(t *testing.T) {
	c := resRepo(t)
	c.MustLore("resource", "kind", "add", "host")
	for _, tc := range []struct {
		field, typ string
		ok         bool
		exit       int
		say        []string
	}{
		{field: "provider", typ: "text", ok: true},
		{field: "token", typ: "secret_ref", ok: true},
		{field: "Bad", typ: "text", exit: resExitInvalid, say: []string{"E_RESOURCE_NAME", "not snake_case"}},
		{field: "password", typ: "text", exit: resExitInvalid, say: []string{"E_RESOURCE_NAME", "looks like a credential", "--type secret_ref"}},
		{field: "api_key", typ: "text", exit: resExitInvalid, say: []string{"E_RESOURCE_NAME", "looks like a credential"}},
		{field: "f1", typ: "nope", exit: resExitInvalid, say: []string{"E_INVALID_INPUT", "is not a value type", "secret_ref"}},
		{field: "provider", typ: "text", exit: resExitInvalid, say: []string{"already has a field \"provider\""}},
	} {
		t.Run(tc.field+"_"+tc.typ, func(t *testing.T) {
			r := c.Lore("resource", "field", "add", "host", tc.field, "--type", tc.typ)
			if tc.ok {
				r.OK(t).Says(t, "added field fld_", "host."+tc.field)
				return
			}
			r.Fails(t, tc.exit, tc.say...)
		})
	}
	c.Lore("resource", "field", "add", "nokind", "f1", "--type", "text").
		Fails(t, resExitKind, "E_KIND_UNKNOWN", "lore resource kinds")
	// secret_ref is internal by default and says so.
	c.Lore("resource", "field", "add", "host", "vault_entry", "--type", "secret_ref").OK(t).Says(t, "internal by default")
}

// Every value type accepts a value of its shape and refuses one that is
// not, naming the field (E_RESOURCE_INVALID, exit 20); the accepted value
// reads back through show --json.
func TestResourcesValueTypeMatrix(t *testing.T) {
	c := resRepo(t)
	c.MustLore("resource", "kind", "add", "thing")
	c.MustLore("resource", "kind", "add", "peer")
	c.MustLore("resource", "add", "peer", "p1")
	for _, tc := range []struct {
		typ, good, bad string
		// want is the stored canonical form when it differs from good
		// (a datetime keeps nanoseconds, a duration is Go's text).
		want  string
		extra []string
	}{
		{typ: "text", good: "hello world"},
		{typ: "number", good: "42", bad: "many"},
		{typ: "bool", good: "true", bad: "maybe"},
		{typ: "date", good: "2026-11-07", bad: "07/11/2026"},
		{typ: "datetime", good: "2026-11-07T10:00:00Z", want: "2026-11-07T10:00:00.000000000Z", bad: "tomorrow"},
		{typ: "duration", good: "30d", want: "720h0m0s", bad: "3y"}, // #22b: the --remind-before grammar
		{typ: "ip", good: "10.0.0.1", bad: "10.0.0.300"},
		{typ: "cidr", good: "10.0.0.0/24", bad: "10.0.0.0"},
		{typ: "dns", good: "api.example.com", bad: "not a host"},
		{typ: "url", good: "https://example.com/x", bad: "example"},
		{typ: "email", good: "person@example.com", bad: "person"},
		{typ: "ref", good: "p1", bad: "no_such_peer", extra: []string{"--ref-kind", "peer"}},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			field := "f_" + tc.typ
			c.Lore(append([]string{"resource", "field", "add", "thing", field, "--type", tc.typ}, tc.extra...)...).OK(t)
			name := "ok_" + tc.typ
			c.Lore("resource", "add", "thing", name, "--set", field+"="+tc.good).OK(t)
			want := tc.good
			if tc.want != "" {
				want = tc.want
			}
			if got := resShow(t, c, name).Values[field]; got != want {
				t.Fatalf("%s value read back as %q, want %q", tc.typ, got, want)
			}
			if tc.bad == "" {
				return
			}
			r := c.Lore("resource", "add", "thing", "bad_"+tc.typ, "--set", field+"="+tc.bad)
			r.Fails(t, resExitInvalid, field)
			if !strings.Contains(r.Out(), "E_RESOURCE_INVALID") && !strings.Contains(r.Out(), "E_INVALID_INPUT") {
				t.Fatalf("a %s value that does not fit names a value error: %s", tc.typ, r)
			}
			c.Lore("resource", "show", "bad_"+tc.typ).Fails(t, resExitNotFound, "E_NOT_FOUND")
		})
	}
}

// Closed value sets, required fields and number bounds: the refusal names
// the field and the allowed values, the kind show hint, and the kind's
// --json shape carries all three.
func TestResourcesFieldRulesAndKindJSON(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.Lore("resource", "add", "host", "h1", "--set", "provider=gcp").
		Fails(t, resExitInvalid, "E_RESOURCE_INVALID", `provider "gcp" is not one of hetzner, aws`, "lore resource kind show --id host")
	c.Lore("resource", "add", "host", "h1").
		Fails(t, resExitInvalid, "E_RESOURCE_INVALID", "required field provider is missing")
	c.MustLore("resource", "add", "host", "h1", "--set", "provider=aws")
	for _, port := range []string{"0", "65536", "-1"} {
		c.Lore("resource", "add", "service", "svc_"+strings.TrimPrefix(port, "-"), "--set", "port="+port).
			Fails(t, resExitInvalid, "port")
	}
	for _, port := range []string{"1", "65535"} { // the bounds are inclusive
		c.MustLore("resource", "add", "service", "svc_"+port, "--set", "port="+port, "--set", "runs_on=h1")
	}
	k := th.DecodeJSON[kindShowJSON](t, c.Lore("resource", "kind", "show", "service", "--json")).Data
	if k.Name != "service" || !strings.HasPrefix(k.ID, "knd_") || k.Store != "repo" || k.Count != 2 {
		t.Fatalf("kind show --json = %+v", k)
	}
	byName := map[string]fieldJSON{}
	for _, f := range k.Fields {
		byName[f.Name] = f
	}
	if f := byName["port"]; f.Type != "number" || f.Min != "1" || f.Max != "65535" {
		t.Fatalf("port field = %+v", f)
	}
	if f := byName["runs_on"]; f.Type != "ref" || f.RefKind != "host" {
		t.Fatalf("runs_on field = %+v", f)
	}
	h := th.DecodeJSON[kindShowJSON](t, c.Lore("resource", "kind", "show", "host", "--json")).Data
	for _, f := range h.Fields {
		if f.Name == "provider" && (!f.Required || !slices.Equal(f.Values, []string{"hetzner", "aws"})) {
			t.Fatalf("provider field = %+v", f)
		}
	}
	if h.Description != "a server" || h.Count != 1 {
		t.Fatalf("host kind = %+v", h)
	}
}

// Removing a field rows still use is refused (E_RESOURCE_IN_USE, exit 23)
// with the --drop way out; --drop removes the values with the field; an
// unused field goes at once.
func TestResourcesFieldRemoveInUse(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "h1", "--set", "provider=aws", "--set", "ip=10.0.0.1")
	c.Lore("resource", "field", "remove", "host", "ip").
		Fails(t, resExitRefused, "E_RESOURCE_IN_USE", "host.ip has values on 1 row", "--drop")
	if got := resShow(t, c, "h1").Values["ip"]; got != "10.0.0.1" {
		t.Fatalf("a refused remove keeps the value; ip = %q", got)
	}
	c.MustLore("resource", "field", "remove", "host", "cores", "--yes") // unused
	c.MustLore("resource", "field", "remove", "host", "ip", "--drop", "--yes")
	if v, ok := resShow(t, c, "h1").Values["ip"]; ok {
		t.Fatalf("--drop removes the values; ip = %q", v)
	}
	c.Lore("resource", "edit", "h1", "--set", "ip=10.0.0.2").Fails(t, resExitInvalid, "ip")
}
