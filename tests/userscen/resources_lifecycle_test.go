//go:build userscen

// One resource's whole life as a person drives it: add → show → edit →
// rename → delete (trash) → show says it is in the trash → trash restore
// (same id) → purge; the --json shapes of add/edit/delete/show/list; a
// name used twice (E_RESOURCE_TAKEN); every way
// to name a resource the noun's help promises (bare name, res_ id,
// <kind>/<name>, <place>:<kind>/<name>); resource name rules (snake_case,
// unicode, spaces, reserved-looking words, 128-character cap, res_-shaped
// names); archive refusing new refs; list --where/--sort.
package userscen

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestResourcesLifecycleAddToPurge(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	add := th.DecodeJSON[resWriteJSON](t, c.Lore("resource", "add", "host", "web1", "--set", "provider=aws", "--set", "ip=10.0.0.1", "--json"))
	if !strings.HasPrefix(add.ID, "res_") || add.Store != "repo" {
		t.Fatalf("add --json = %+v", add)
	}
	s := resShow(t, c, "web1")
	if s.ID != add.ID || s.Kind != "host" || s.Name != "web1" || s.Store != "repo" || s.Values["provider"] != "aws" || s.Values["ip"] != "10.0.0.1" {
		t.Fatalf("show --json = %+v", s)
	}
	if s.LastChecked != "" {
		t.Fatalf("an add does not stamp last_checked: %+v", s)
	}
	// An edit stamps last_checked and keeps the id.
	ed := th.DecodeJSON[resWriteJSON](t, c.Lore("resource", "edit", "web1", "--set", "provider=hetzner", "--unset", "ip", "--json"))
	s = resShow(t, c, "web1")
	if ed.ID != add.ID || s.Values["provider"] != "hetzner" || s.Values["ip"] != "" || s.LastChecked == "" {
		t.Fatalf("after edit: %+v / %+v", ed, s)
	}
	// Rename keeps the id and refs.
	c.MustLore("resource", "add", "service", "api", "--set", "runs_on=web1")
	c.MustLore("resource", "rename", "web1", "web_one").Says(t, "web1 → web_one", "ids and refs unchanged")
	if got := resShow(t, c, "web_one").ID; got != add.ID {
		t.Fatalf("rename changed the id: %s → %s", add.ID, got)
	}
	if got := resShow(t, c, "api").Values["runs_on"]; got != "web_one" {
		t.Fatalf("a ref shows the renamed target: %q", got)
	}
	c.Lore("resource", "show", "web1").Fails(t, resExitNotFound, "E_NOT_FOUND")
	// Delete → trash; the row answers E_IN_TRASH with the restore command.
	c.MustLore("resource", "delete", "web_one", "--yes").Says(t, "moved to the trash", "lore trash restore --id "+add.ID, "still pointing at it: api")
	c.Lore("resource", "show", "web_one").Fails(t, resExitRefused, "E_IN_TRASH", "lore trash restore --id "+add.ID)
	if l := resList(t, c, "host"); l.Count != 0 {
		t.Fatalf("a trashed row leaves list: %+v", l)
	}
	if l := resList(t, c, "host", "--deleted-only"); l.Count != 1 || l.Data[0].ID != add.ID {
		t.Fatalf("--deleted-only lists the trashed row: %+v", l)
	}
	// A name held by a row in the trash: see bugs.md (add writes a second
	// row; the docs say trashed rows keep the name taken).
	c.MustLore("trash", "restore", add.ID).Says(t, "restored 1 row")
	if s := resShow(t, c, "web_one"); s.ID != add.ID || s.Values["provider"] != "hetzner" {
		t.Fatalf("restore brings back the same id and values: %+v", s)
	}
	// Delete again and purge for good.
	c.MustLore("resource", "delete", "web_one", "--yes")
	c.Lore("trash", "purge", add.ID).Refused(t, "--confirm")
	c.MustLore("trash", "purge", add.ID, "--confirm")
	c.Lore("trash", "restore", add.ID).Refused(t)
}

// Adding a name that is already used in this place is refused with
// E_RESOURCE_TAKEN (exit 23); the same name in another kind is fine.
func TestResourcesNameTaken(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "api", "--set", "provider=aws")
	r := c.Lore("resource", "add", "host", "api", "--set", "provider=aws", "--json")
	if e := resErrOf(t, r); e.Error.Code != "E_RESOURCE_TAKEN" || e.Error.Exit != resExitRefused || !strings.Contains(e.Error.Message, "host api already exists in repo") {
		t.Fatalf("taken --json = %+v", e)
	}
	c.MustLore("resource", "add", "service", "api", "--set", "runs_on=api")
	// A bare name now matches two kinds: show says which and how to pick.
	c.Lore("resource", "show", "api").Fails(t, resExitAmbig, "E_RESOURCE_AMBIGUOUS", "res_ id")
	if s := resShow(t, c, "service/api"); s.Kind != "service" {
		t.Fatalf("service/api names the service: %+v", s)
	}
	if s := resShow(t, c, "host/api"); s.Kind != "host" {
		t.Fatalf("host/api names the host: %+v", s)
	}
}

// Every way the noun's help promises to name a resource: a bare name,
// its res_ id, <kind>/<name> and <place>:<kind>/<name> (repo is the
// place lore itself prints after add). (Bug found by this suite: the
// qualified forms answered E_NOT_FOUND although add prints
// repo:<kind>/<name>.)
func TestResourcesQualifiedNames(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	out := c.MustLore("resource", "add", "host", "hub_prod", "--set", "provider=aws").Stdout
	if !strings.Contains(out, "repo:host/hub_prod") {
		t.Fatalf("add prints the qualified name: %s", out)
	}
	id := resShow(t, c, "hub_prod").ID
	for _, key := range []string{"hub_prod", id, "host/hub_prod", "repo:host/hub_prod"} {
		if got := resShow(t, c, key).ID; got != id {
			t.Fatalf("show %s = %s, want %s", key, got, id)
		}
	}
	// The qualified name works for every verb taking <name>, and in a ref.
	c.MustLore("resource", "edit", "repo:host/hub_prod", "--set", "cores=4")
	c.MustLore("resource", "history", "host/hub_prod")
	c.MustLore("resource", "graph", "repo:host/hub_prod", "--up")
	for _, key := range []string{"service/hub_prod", "repo:service/hub_prod", "nokind/hub_prod", "repo:host/nope", "elsewhere:host/hub_prod", ":host/hub_prod", "host/"} {
		c.Lore("resource", "show", key).Fails(t, resExitNotFound, "E_NOT_FOUND")
	}
}

// Resource names: snake_case under the default name format, a 128
// character cap, names shaped like commands or ids are plain names.
func TestResourcesResourceNameMatrix(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	long := strings.Repeat("a", 128)
	for _, tc := range []struct {
		name, label string
		ok          bool
		exit        int
		say         []string
	}{
		{name: "web_1", label: "snake", ok: true},
		{name: "a__b", label: "double underscore", ok: true},
		{name: "select", label: "sql word", ok: true},
		{name: "list", label: "command word", ok: true},
		{name: "res_abc", label: "id-shaped", ok: true},
		{name: long, label: "128 chars", ok: true},
		{name: long + "a", label: "129 chars", exit: resExitInvalid, say: []string{"E_RESOURCE_NAME", "at most 128 characters"}},
		{name: "Web", label: "upper", exit: resExitInvalid, say: []string{"E_RESOURCE_NAME", "name format", "lore resource kind show --id host"}},
		{name: "wéb", label: "unicode", exit: resExitInvalid, say: []string{"E_RESOURCE_NAME"}},
		{name: "a b", label: "space", exit: resExitInvalid, say: []string{"E_RESOURCE_NAME"}},
		{name: "a-b", label: "dash", exit: resExitInvalid, say: []string{"E_RESOURCE_NAME"}},
		{name: "_a", label: "leading underscore", exit: resExitInvalid, say: []string{"E_RESOURCE_NAME"}},
		{name: "1a", label: "leading digit", exit: resExitInvalid, say: []string{"E_RESOURCE_NAME"}},
		{name: "", label: "empty", exit: resExitInvalid, say: []string{"E_INVALID_INPUT", "missing required field name"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			r := c.Lore("resource", "add", "host", tc.name, "--set", "provider=aws")
			if !tc.ok {
				r.Fails(t, tc.exit, tc.say...)
				return
			}
			r.OK(t)
			if got := resShow(t, c, tc.name).Name; got != tc.name {
				t.Fatalf("show %q = %q", tc.name, got)
			}
		})
	}
	// A dns-format kind takes host names, an ip-format kind addresses.
	c.MustLore("resource", "kind", "add", "domain", "--name-format", "dns")
	c.MustLore("resource", "add", "domain", "api.example.com")
	c.Lore("resource", "add", "domain", "not a domain").Fails(t, resExitInvalid, "E_RESOURCE_NAME")
	c.MustLore("resource", "kind", "add", "address", "--name-format", "ip")
	c.MustLore("resource", "add", "address", "2001:db8::1")
	if got := resShow(t, c, "2001:db8::1").Kind; got != "address" {
		t.Fatalf("an IPv6 name (holding ':') resolves as itself: %q", got)
	}
	c.Lore("resource", "add", "address", "10.0.0.256").Fails(t, resExitInvalid, "E_RESOURCE_NAME")
	c.Lore("resource", "kind", "add", "weird", "--name-format", "uuid").Fails(t, resExitInvalid)
}

// Archive: the row stays and old refs stay, a new ref is refused with
// E_RESOURCE_ARCHIVED (exit 23); list hides it unless --archived.
func TestResourcesArchiveRefusesNewRefs(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "old", "--set", "provider=aws")
	c.MustLore("resource", "add", "service", "s1", "--set", "runs_on=old")
	c.MustLore("resource", "archive", "old").Says(t, "archived old")
	c.Lore("resource", "add", "service", "s2", "--set", "runs_on=old").Fails(t, resExitRefused, "E_RESOURCE_ARCHIVED", "old is archived")
	if got := resShow(t, c, "s1").Values["runs_on"]; got != "old" {
		t.Fatalf("an old ref stays: %q", got)
	}
	if l := resList(t, c, "host"); l.Count != 0 {
		t.Fatalf("list hides archived rows: %+v", l)
	}
	if l := resList(t, c, "host", "--archived"); l.Count != 1 {
		t.Fatalf("--archived lists them: %+v", l)
	}
}

// list --where and --sort: each operator, an unknown field, and sorting
// both ways.
func TestResourcesListWhereSort(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	for i, p := range []string{"aws", "hetzner", "aws"} {
		c.MustLore("resource", "add", "host", []string{"h_a", "h_b", "h_c"}[i], "--set", "provider="+p, "--set", "cores="+[]string{"2", "8", "4"}[i])
	}
	names := func(l resListJSON) string {
		var out []string
		for _, r := range l.Data {
			out = append(out, r.Name)
		}
		return strings.Join(out, ",")
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--where", "provider=aws"}, "h_a,h_c"},
		{[]string{"--where", "provider!=aws"}, "h_b"},
		{[]string{"--where", "cores>2"}, "h_b,h_c"},
		{[]string{"--where", "cores<=4"}, "h_a,h_c"},
		{[]string{"--where", "provider:aws,hetzner", "--where", "cores>=8"}, "h_b"},
		{[]string{"--where", "ip?"}, ""},
		{[]string{"--where", "ip!?"}, "h_a,h_b,h_c"},
		{[]string{"--sort", "cores:desc"}, "h_b,h_c,h_a"},
		{[]string{"--sort", "cores"}, "h_a,h_c,h_b"},
		{[]string{"--sort", "name:desc"}, "h_c,h_b,h_a"},
	} {
		if got := names(resList(t, c, "host", tc.args...)); got != tc.want {
			t.Errorf("list %v = %q, want %q", tc.args, got, tc.want)
		}
	}
	c.Lore("resource", "list", "host", "--where", "nofield=1").Fails(t, resExitInvalid, "nofield")
	c.Lore("resource", "list", "host", "--sort", "nofield").Fails(t, resExitInvalid, "nofield")
	c.Lore("resource", "list", "nokind").Fails(t, resExitKind, "E_KIND_UNKNOWN")
}
