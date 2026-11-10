//go:build userscen

package userscen

// campaign_data_resref_test.go — a ${secret:…} reference written inside a
// resource's text value is part of the secret inventory like one in any
// text field: secrets list and show find it, rename-entry rewrites it in
// place (the rest of the text kept byte for byte), and a secret_ref field
// beside it is still handled as before.

import (
	"strings"
	"testing"
)

// TestCampaignData_SecretRefInResourceText: list → show → rename-entry →
// list, for a reference in a text field and a bare one in a secret_ref
// field of the same resource; the other fields survive every rewrite.
func TestCampaignData_SecretRefInResourceText(t *testing.T) {
	c := resRepo(t)
	c.MustLore("resource", "kind", "add", "server")
	c.MustLore("resource", "field", "add", "server", "notes", "--type", "text")
	c.MustLore("resource", "field", "add", "server", "root", "--type", "secret_ref")
	c.MustLore("resource", "field", "add", "server", "provider", "--type", "text", "--values", "hetzner,aws")
	c.MustLore("resource", "add", "server", "web9", "--set", "provider=hetzner",
		"--set", "notes=console login at ${secret:gopass://acme/web9/console}, ask ops",
		"--set", "root=gopass://acme/web9/root")
	id := resShow(t, c, "server/web9").ID
	l := c.MustLore("secrets", "list", "--json").Stdout
	for _, want := range []string{"gopass://acme/web9/console", "gopass://acme/web9/root"} {
		if !strings.Contains(l, want) {
			t.Errorf("secrets list misses %s: %s", want, l)
		}
	}
	c.MustLore("secrets", "show", id).Says(t, "acme/web9/console", "acme/web9/root")
	c.MustLore("secrets", "rename-entry", "--manager", "gopass", "--from", "acme/web9/console", "--to", "acme/web9/web-console").Says(t, "Renamed 1")
	got := resShow(t, c, "server/web9").Values
	if got["notes"] != "console login at ${secret:gopass://acme/web9/web-console}, ask ops" {
		t.Errorf("rename-entry rewrote the text value as %q", got["notes"])
	}
	if got["root"] != "gopass://acme/web9/root" {
		t.Errorf("rename-entry touched the other field: %q", got["root"])
	}
	c.MustLore("secrets", "rename-entry", "--manager", "gopass", "--from", "acme/web9/root", "--to", "acme/web9/admin").Says(t, "Renamed 1")
	after := resShow(t, c, "server/web9").Values
	if after["root"] != "gopass://acme/web9/admin" {
		t.Errorf("secret_ref field after rename-entry: %q", after["root"])
	}
	// the fields the rewrite did not touch stay (they were wiped)
	if after["provider"] != "hetzner" || !strings.Contains(after["notes"], "web-console") {
		t.Errorf("rename-entry dropped the other fields: %v", after)
	}
}
