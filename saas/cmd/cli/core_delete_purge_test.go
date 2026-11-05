package main

import (
	"strings"
	"testing"
)

// PASS3 / FA-DEL-1: resource delete and meta delete take --purge/--confirm
// like every core delete. Matrix per noun: --purge alone and --purge --yes
// refuse with E_NEEDS_CONFIRM (exit 26) and leave the row; --confirm alone
// is a soft delete with the purge hint (row in the trash); --purge
// --confirm tombstones (row not in the trash, "purged for good").
func TestResourceMetaDeletePurgeConfirm(t *testing.T) {
	r := newToolRepo(t, nil)
	r.lore("init")
	r.lore("resource", "kind", "add", "host")
	r.lore("resource", "field", "add", "host", "provider", "--type", "text")
	for _, n := range []string{"h_soft", "h_gone", "h_bulk"} {
		r.lore("resource", "add", "host", n)
	}
	r.lore("resource", "edit", "h_bulk", "--set", "provider=x")
	r.lore("meta", "add", "k_soft", "--type", "text")
	r.lore("meta", "add", "k_gone", "--type", "text", "--values", "a,b")

	run := func(args ...string) result {
		t.Helper()
		return r.w.run(r.dir, []string{"LORE_ACTOR=human:tools@example.com"}, e2eBin, args...)
	}
	cases := []struct{ noun, soft, gone string }{
		{"resource", "h_soft", "h_gone"},
		{"meta", "k_soft", "k_gone"},
	}
	for _, c := range cases {
		for _, refused := range [][]string{{"--purge"}, {"--purge", "--yes"}} {
			res := run(append([]string{c.noun, "delete", c.gone}, refused...)...)
			if res.code != 26 || !strings.Contains(res.stderr, "E_NEEDS_CONFIRM") || !strings.Contains(res.stderr, "--confirm") {
				t.Fatalf("%s delete %v: want E_NEEDS_CONFIRM exit 26, got %d\n%s%s", c.noun, refused, res.code, res.stdout, res.stderr)
			}
		}
		if res := run(c.noun, "delete", c.soft, "--confirm"); res.code != 0 ||
			!strings.Contains(res.stdout, "moved to the trash") || !strings.Contains(res.stdout, "lore trash purge <id> --confirm") {
			t.Fatalf("%s delete --confirm: want soft delete + purge hint, got %d\n%s%s", c.noun, res.code, res.stdout, res.stderr)
		}
		if res := run(c.noun, "delete", c.gone, "--purge", "--confirm"); res.code != 0 || !strings.Contains(res.stdout, "purged for good") {
			t.Fatalf("%s delete --purge --confirm: want purged, got %d\n%s%s", c.noun, res.code, res.stdout, res.stderr)
		}
	}
	// Bulk form: the refusal names the matches, then the confirmed purge.
	if res := run("resource", "delete", "--kind", "host", "--where", "provider=x", "--purge"); res.code != 26 || !strings.Contains(res.stderr, "every matching row") {
		t.Fatalf("bulk --purge without --confirm: %d %s", res.code, res.stderr)
	}
	if res := run("resource", "delete", "--kind", "host", "--where", "provider=x", "--purge", "--confirm"); res.code != 0 || !strings.Contains(res.stdout, "purged for good") {
		t.Fatalf("bulk --purge --confirm: %d %s%s", res.code, res.stdout, res.stderr)
	}
	trash := r.lore("trash").stdout
	for _, want := range []string{`"h_soft"`, `"k_soft"`} {
		if !strings.Contains(trash, want) {
			t.Errorf("trash lacks the soft-deleted %s:\n%s", want, trash)
		}
	}
	for _, gone := range []string{`"h_gone"`, `"k_gone"`, `"h_bulk"`} {
		if strings.Contains(trash, gone) {
			t.Errorf("purged %s is still in the trash:\n%s", gone, trash)
		}
	}
}
