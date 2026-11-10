//go:build userscen

package userscen

// `lore config get|set|list|check` (docs/reference/config.md): an empty
// config, set then get (human and --json), a bad value and an unknown key
// refused with "nothing written" and the file unchanged, a typo key is a
// check warning, an invalid YAML file stops check and every reader with
// E_CONFIG_INVALID and the next step.

import (
	"os"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestSecretsConfigGetSetCheck(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	a := w.Human(secPersonName, secPersonEmail)
	dir := w.Root
	a.Lore(dir, "config", "list").OK(t).Says(t, "(no config keys)")
	a.Lore(dir, "config", "get", "live.autopush").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "lore config list")
	a.Lore(dir, "config", "set", "live.autopush", "false").OK(t).Says(t, "live.autopush = false")
	a.Lore(dir, "config", "get", "live.autopush").OK(t).Says(t, "false")
	g := th.DecodeJSON[struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}](t, a.Lore(dir, "config", "get", "live.autopush", "--json"))
	if g.Key != "live.autopush" || g.Value != "false" {
		t.Fatalf("get --json: %+v", g)
	}
	l := th.DecodeJSON[struct {
		Count   int `json:"count"`
		Entries []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"entries"`
	}](t, a.Lore(dir, "config", "list", "--json"))
	if l.Count != 1 || l.Entries[0].Key != "live.autopush" {
		t.Fatalf("list --json: %+v", l)
	}
	// Set twice: the same value, then another.
	a.Lore(dir, "config", "set", "live.autopush", "false").OK(t)
	a.Lore(dir, "config", "set", "live.autopush", "true").OK(t)
	a.Lore(dir, "config", "get", "live.autopush").OK(t).Says(t, "true")

	path := a.LorePath("config.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a.Lore(dir, "config", "set", "live.autopush", "maybe").Fails(t, 20, "E_INVALID_INPUT", `bad value "maybe"`, "nothing written")
	a.Lore(dir, "config", "set", "live.autopsh", "true").Fails(t, 20, "is not a key lore reads", "nothing written", "lore policy set")
	a.Lore(dir, "config", "set", "nosuch.key", "1").Fails(t, 20, "is not a key lore reads")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a refused set changed the file:\n%s\n->\n%s", before, after)
	}
	a.Lore(dir, "config", "check").OK(t).Says(t, "is valid")

	// A comment and key order survive a set.
	a.WriteHomeFile(".lore/config.yaml", "# my laptop\nlive:\n  autopush: false\n  max_age: 3h\n")
	a.Lore(dir, "config", "set", "live.autopush", "true").OK(t)
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "# my laptop\n") || strings.Index(string(b), "autopush") > strings.Index(string(b), "max_age") {
		t.Fatalf("set must keep comments and order:\n%s", b)
	}

	// A typo is a warning, not an error.
	a.WriteHomeFile(".lore/config.yaml", "live:\n  autopush: false\n  max_agee: 3h\n")
	c := th.DecodeJSON[struct {
		Exists   bool     `json:"exists"`
		Warnings []string `json:"warnings"`
	}](t, a.Lore(dir, "config", "check", "--json"))
	if !c.Exists || len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "max_agee") {
		t.Fatalf("check --json warnings: %+v", c)
	}
	// Invalid YAML: check and readers refuse with the next step.
	a.WriteHomeFile(".lore/config.yaml", "live: [\n")
	a.Lore(dir, "config", "check").Fails(t, 20, "E_CONFIG_INVALID", "not valid YAML")
	a.Lore(dir, "config", "get", "live.autopush").Fails(t, 20, "E_CONFIG_INVALID", "lore config check")
	a.Lore(dir, "config", "set", "live.autopush", "true").Refused(t, "E_CONFIG_INVALID")
}
