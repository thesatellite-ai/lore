package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestOrgForStoreURLAliasSpelling: the entry org import made records the
// canonical url (git@github.com:…) of an ssh alias answer; the join looks
// it up by the alias spelling it was given, on a machine whose ssh config
// does not name the alias (devco D-644: "org import of git@<alias>:… made
// no org entry" although it had made it).
func TestOrgForStoreURLAliasSpelling(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no ~/.ssh/config: the alias is not resolved
	home := t.TempDir()
	cfg := "orgs:\n  - name: acme\n    stores:\n      - url: git@github.com:acme-co/lore-team.git\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{
		"git@github.com:acme-co/lore-team.git",
		"git@github-acme:acme-co/lore-team.git",
		"https://github.com/acme-co/lore-team",
	} {
		if got := orgForStoreURL(home, u); got != "acme" {
			t.Errorf("orgForStoreURL(%q) = %q, want acme", u, got)
		}
	}
	for _, u := range []string{"git@github-acme:acme-co/other.git", "git@gitlab.com:acme-co/lore-team.git"} {
		if got := orgForStoreURL(home, u); got != "" {
			t.Errorf("orgForStoreURL(%q) = %q, want no entry", u, got)
		}
	}
}

// TestHomeGuardUndo: a refused or failed join leaves the home as it was:
// config.yaml back to its bytes, a store-clones.json it made removed, a
// clone folder it made removed (with a new owner folder), while what was
// there before stays (devco D-644).
func TestHomeGuardUndo(t *testing.T) {
	home := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.yaml", "orgs: []\n")
	write("stores/_me/lore-store.yaml", "me\n")
	write("stores/acme-co/kept/README.md", "kept\n")
	g := guardHome(home)
	// what a half-applied join wrote
	write("config.yaml", "orgs:\n  - name: acme\n")
	write("state/store-clones.json", "{}\n")
	write("stores/acme-co/lore-team/README.md", "new clone\n")
	write("stores/acme-labs/lore-team/README.md", "new owner\n")
	if err := g.failed(nil); err != nil {
		t.Fatal(err)
	}
	if err := g.undo(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "config.yaml")); string(b) != "orgs: []\n" {
		t.Errorf("config.yaml = %q, want it back as it was", b)
	}
	for _, gone := range []string{"state/store-clones.json", "stores/acme-co/lore-team", "stores/acme-labs"} {
		if _, err := os.Stat(filepath.Join(home, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there after undo", gone)
		}
	}
	for _, kept := range []string{"stores/_me/lore-store.yaml", "stores/acme-co/kept/README.md"} {
		if _, err := os.Stat(filepath.Join(home, kept)); err != nil {
			t.Errorf("%s was there before and is gone: %v", kept, err)
		}
	}
}

// TestExistingEntryJoinHint: a setup join refused for an existing entry
// of the org's name names the join answer that uses it, never `--as`,
// which setup apply does not have (devco D-644).
func TestExistingEntryJoinHint(t *testing.T) {
	h := existingEntryJoinHint("acme", &orgconf.Org{Name: "acme", Stores: []orgconf.Store{{URL: "git@github.com:acme-co/lore-team.git"}}})
	for _, w := range []string{`--answers '{"invite":"acme"}'`, "git@github.com:acme-co/lore-team.git"} {
		if !strings.Contains(h, w) {
			t.Errorf("hint %q lacks %q", h, w)
		}
	}
	if strings.Contains(h, "--as") {
		t.Errorf("hint %q names --as", h)
	}
}
