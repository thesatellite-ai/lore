//go:build userscen

// Closed value sets and team policy on resources: add, rename (rows that
// hold the value show the new name at once, ids kept), remove (refused
// while used, E_RESOURCE_IN_USE; allowed once unused), value labels as
// input; a required field made optional and back; a field default; the
// resources.allowed_in policy refusing a write in a place it excludes
// (E_RESOURCE_PLACE, exit 23) and allowing it again after unset; the
// clone verb copying values with --set overrides; bulk edit and bulk
// delete with --where, --dry-run first.
package userscen

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestResourcesValueSetLifecycle(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "h1", "--set", "provider=aws")
	c.MustLore("resource", "add", "host", "h2", "--set", "provider=hetzner")
	id := resShow(t, c, "h1").ID
	c.MustLore("resource", "field", "edit", "host", "provider", "--add-value", "gcp")
	c.MustLore("resource", "add", "host", "h3", "--set", "provider=gcp")
	c.MustLore("resource", "field", "edit", "host", "provider", "--rename-value", "aws=amazon").Says(t, "renamed 1 value;")
	if s := resShow(t, c, "h1"); s.ID != id || s.Values["provider"] != "amazon" {
		t.Fatalf("a renamed value shows on the row at once, same id: %+v", s)
	}
	c.Lore("resource", "add", "host", "h4", "--set", "provider=aws").Fails(t, resExitInvalid, `"aws" is not one of`)
	if l := resList(t, c, "host", "--where", "provider=amazon"); l.Count != 1 || l.Data[0].Name != "h1" {
		t.Fatalf("--where on the renamed value: %+v", l)
	}
	c.Lore("resource", "field", "edit", "host", "provider", "--remove-value", "hetzner").
		Fails(t, resExitRefused, "E_RESOURCE_IN_USE", `value "hetzner" is used by 1 resource`, "nothing written")
	c.MustLore("resource", "edit", "h2", "--set", "provider=gcp")
	c.MustLore("resource", "field", "edit", "host", "provider", "--remove-value", "hetzner")
	k := th.DecodeJSON[kindShowJSON](t, c.Lore("resource", "kind", "show", "host", "--json")).Data
	for _, f := range k.Fields {
		if f.Name == "provider" && strings.Join(f.Values, ",") != "amazon,gcp" {
			t.Fatalf("value set after add/rename/remove = %v", f.Values)
		}
	}
	// A value label is accepted as input and stored as the value.
	c.MustLore("resource", "field", "edit", "host", "provider", "--value-label", "gcp=Google Cloud")
	c.MustLore("resource", "add", "host", "h5", "--set", "provider=Google Cloud")
	if got := resShow(t, c, "h5").Values["provider"]; got != "gcp" {
		t.Fatalf("a label input stores the value: %q", got)
	}
	// Renaming to a value that exists, or a value that does not, is refused.
	c.Lore("resource", "field", "edit", "host", "provider", "--rename-value", "amazon=gcp").Fails(t, resExitInvalid)
	c.Lore("resource", "field", "edit", "host", "provider", "--rename-value", "nope=x").Refused(t, "nope")
}

func TestResourcesRequiredAndDefault(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "field", "edit", "host", "provider", "--no-required")
	c.MustLore("resource", "add", "host", "bare")
	c.MustLore("resource", "field", "add", "host", "os", "--type", "text", "--default", "linux")
	c.MustLore("resource", "add", "host", "d1")
	if got := resShow(t, c, "d1").Values["os"]; got != "linux" {
		t.Fatalf("a field default is written on add: %q", got)
	}
	c.MustLore("resource", "add", "host", "d2", "--set", "os=bsd")
	if got := resShow(t, c, "d2").Values["os"]; got != "bsd" {
		t.Fatalf("a set value wins over the default: %q", got)
	}
	// Making it required again while a row misses it: refused or listed by
	// lint, never a silent pass.
	r := c.Lore("resource", "field", "edit", "host", "provider", "--required")
	if r.Code == 0 {
		c.MustLore("resource", "lint").Says(t, "bare", "provider")
	} else {
		r.Refused(t, "provider")
	}
}

func TestResourcesAllowedInPolicy(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("policy", "show").Says(t, "resources.allowed_in")
	c.MustLore("policy", "set", "resources.allowed_in", "store", "--yes")
	c.Lore("resource", "add", "host", "p1", "--set", "provider=aws").
		Fails(t, resExitRefused, "E_RESOURCE_PLACE", "resources are not allowed in repo places", "resources.allowed_in: store", "lore policy show --id resources")
	f := resWriteFile(t, c, "p.yaml", "resources:\n  - kind: host\n    name: p2\n    fields:\n      provider: aws\n")
	c.Lore("resource", "import", f, "--yes").Fails(t, resExitRefused, "E_RESOURCE_PLACE")
	if n := resList(t, c, "host").Count; n != 0 {
		t.Fatalf("the policy refused every write; %d rows", n)
	}
	c.MustLore("policy", "unset", "resources.allowed_in", "--yes")
	c.MustLore("resource", "add", "host", "p1", "--set", "provider=aws")
	c.Lore("policy", "set", "resources.allowed_in", "nowhere", "--yes").Refused(t)
}

func TestResourcesCloneAndBulk(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "base", "--set", "provider=aws", "--set", "cores=4")
	c.MustLore("resource", "clone", "base", "copy1", "--set", "cores=8")
	if v := resShow(t, c, "copy1").Values; v["provider"] != "aws" || v["cores"] != "8" {
		t.Fatalf("clone copies values with overrides: %v", v)
	}
	c.Lore("resource", "clone", "base", "copy1").Fails(t, resExitRefused, "E_RESOURCE_TAKEN")
	for _, n := range []string{"b1", "b2", "b3"} {
		c.MustLore("resource", "add", "host", n, "--set", "provider=hetzner")
	}
	c.MustLore("resource", "edit", "--kind", "host", "--where", "provider=hetzner", "--set", "cores=2", "--dry-run").Says(t, "b1", "b2", "b3")
	if l := resList(t, c, "host", "--where", "cores=2"); l.Count != 0 {
		t.Fatalf("--dry-run wrote: %+v", l)
	}
	c.MustLore("resource", "edit", "--kind", "host", "--where", "provider=hetzner", "--set", "cores=2", "--yes").Says(t, "bat_")
	if l := resList(t, c, "host", "--where", "cores=2"); l.Count != 3 {
		t.Fatalf("bulk edit: %+v", l)
	}
	c.MustLore("resource", "delete", "--kind", "host", "--where", "provider=hetzner", "--yes").Says(t, "bat_")
	if l := resList(t, c, "host"); l.Count != 2 {
		t.Fatalf("bulk delete leaves base and copy1: %+v", l)
	}
	// A filter matching nothing deletes nothing; a value outside the closed
	// set is refused rather than matching nothing.
	c.MustLore("resource", "delete", "--kind", "host", "--where", "cores=99", "--yes")
	c.Lore("resource", "delete", "--kind", "host", "--where", "provider=gcp", "--yes").Fails(t, resExitInvalid, `"gcp" is not one of`)
	c.Lore("resource", "delete", "base", "--kind", "host", "--where", "provider=aws", "--yes").Fails(t, resExitInvalid)
}
