//go:build journeys && !windows

package journeys

// cliinput_test.go — the command line answers a mistyped word, a switch
// typed both ways and a closed value it does not know with the exact
// cause and the next step (fix group of CLI input checks). Ola types
// `lore help` with a word that names nothing (it printed the root help
// with exit 0), turns switches off by name (`--no-<switch>` existed for a
// few flags only), lists sessions in a folder that is not a repository
// (exit 1 E_INTERNAL "list sessions: …"), and filters resources by a
// provider that is not one of the kind's values (the refusal listed the
// internal val_ ids beside the names).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJourneyCLIInputChecks(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ola := w.Persona("Ola Typist", "ola@example.test")
	lap := ola.Laptop()
	app := lap.Clone(w.Remote("github.com/ola/shop"), "shop")

	w.Step("day1/help-topic-typo-is-refused", func() {
		app.Lore("init", "--non-interactive", "--name=shop").OK(t)
		app.Lore("help", "zznosuchword").Fails(t, 20, `unknown command "zznosuchword" for "lore"`, "lore --help lists its commands")
		app.Lore("help", "memory", "add").OK(t).Says(t, "lore memory add")
	})

	w.Step("day1/every-switch-has-an-off-form", func() {
		app.Lore("memory", "add", "--body", "turned off by name", "--no-yes", "--no-json").OK(t).Never(t, `"id"`)
		app.Lore("memory", "list", "--no-all-repos").OK(t).Says(t, "turned off by name")
		app.Lore("memory", "list", "--all-repos", "--no-all-repos").Fails(t, 20, "--all-repos and --no-all-repos contradict each other")
		app.Lore("memory", "add", "--body", "x", "--yes", "false").Fails(t, 20, "--yes is a switch and takes no value", "to turn it off give --no-yes")
	})

	w.Step("day1/session-list-outside-a-repo", func() {
		dir := filepath.Join(t.TempDir(), "not-a-repo")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		lap.Lore(dir, "session", "list").Fails(t, 19, "[E_NOT_A_STORE] git: the folder is not inside a git work tree").Never(t, "E_INTERNAL", "list sessions:")
	})

	w.Step("day1/closed-value-refusal-names-values-only", func() {
		app.Lore("resource", "kind", "add", "--kind", "host").OK(t)
		app.Lore("resource", "field", "add", "--kind", "host", "--field", "provider", "--type", "text", "--values", "hetzner,amazon").OK(t)
		app.Lore("resource", "add", "--kind", "host", "--name", "web1", "--set", "provider=amazon").OK(t)
		app.Lore("resource", "list", "--kind", "host", "--where", "provider=amazon").OK(t).Says(t, "web1")
		app.Lore("resource", "list", "--kind", "host", "--where", "provider=nope").Fails(t, 20, `provider "nope" is not one of hetzner, amazon`).Never(t, "val_")
	})
}
