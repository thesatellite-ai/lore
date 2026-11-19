//go:build journeys && !windows

package journeys

// secret_maskedit_test.go — an edit made from a masked read (devco:D-717).
// A note's key that no allow entry accepts reads "[redacted <rule>]". Saving
// `lore row markdown` with a change elsewhere in the body (or `memory edit
// --body` with the text copied from show) stored that mask over the key,
// and `lore trash list` / `trash show` printed the key of a deleted note.
// Now the key stays in the row file under every mask kept as printed, a
// changed mask is refused with the --secret-* way to change a value, and
// no write or trash output prints the key.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyEditFromAMaskedReadKeepsTheKey(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dev := w.Persona("Rae Lindqvist", "rae@masks.example")
	lap := dev.Laptop()
	key := "AKIA" + "QWERTYUIOP2345AB" // made up, real-shaped; built at run time
	var (
		c  *teamharness.Clone
		id string
	)
	rowFile := func() string {
		b, err := os.ReadFile(filepath.Join(c.Dir, ".lore", "data", "memories", id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	keyKept := func(want string) {
		t.Helper()
		if f := rowFile(); !strings.Contains(f, key) || strings.Contains(f, "[redacted") || !strings.Contains(f, want) {
			t.Fatalf("the row file lost the key or holds a mask (want %q):\n%s", want, f)
		}
	}

	w.Step("day1/a-note-whose-key-reads-masked", func() {
		c = lap.Clone(w.Remote("github.com/masks/edit"), "edit")
		lap.LoreIn(c, "init").OK(t)
		lap.LoreIn(c, "memory", "add", "--body", "deploy key "+key+" for staging", "--not-a-secret", "--reason", "a fixture", "--yes").OK(t)
		m, _ := filepath.Glob(filepath.Join(c.Dir, ".lore", "data", "memories", "*.json"))
		if len(m) != 1 {
			t.Fatalf("want one memory file, got %v", m)
		}
		id = strings.TrimSuffix(filepath.Base(m[0]), ".json")
		allows := regexp.MustCompile(`"id": "(sal_[0-9a-f]+)"`).FindAllStringSubmatch(lap.LoreIn(c, "secrets", "allow", "list", "--json").OK(t).Out(), -1)
		if len(allows) == 0 {
			t.Fatal("no allow entry was recorded")
		}
		for _, a := range allows {
			lap.LoreIn(c, "secrets", "allow", "rm", "--id", a[1], "--yes").OK(t)
		}
		lap.LoreIn(c, "show", id).OK(t).Says(t, "[redacted ").Never(t, key)
	})

	var view string
	w.Step("day1/save-the-masked-view", func() {
		view = lap.LoreIn(c, "row", "markdown", "--id", id).OK(t).Says(t, "[redacted ").Never(t, key).Stdout
		lap.LoreIn(c, "row", "edit", "--id", id, "--markdown", view).OK(t).Never(t, key)
		keyKept("for staging")
		lap.LoreIn(c, "row", "edit", "--id", id, "--markdown", strings.Replace(view, "for staging", "for staging and prod", 1), "--json").OK(t).Never(t, key)
		keyKept("for staging and prod")
		shown := lap.LoreIn(c, "show", id).OK(t).Never(t, key).Out()
		masked := regexp.MustCompile(`deploy key (\[redacted [^\]]+\]) for staging and prod`).FindStringSubmatch(shown)
		if masked == nil {
			t.Fatalf("show does not print the masked body:\n%s", shown)
		}
		lap.LoreIn(c, "memory", "edit", "--id", id, "--body", "deploy key "+masked[1]+" for staging and prod (copied from show)").OK(t).Never(t, key)
		keyKept("(copied from show)")
	})

	w.Step("day1/a-changed-mask-is-refused", func() {
		v := lap.LoreIn(c, "row", "markdown", "--id", id).OK(t).Stdout
		changed := regexp.MustCompile(`\[redacted ([^\]]+)\]`).ReplaceAllString(v, "[redacted ${1}x]")
		lap.LoreIn(c, "row", "edit", "--id", id, "--markdown", changed).Refused(t, "E_INVALID_INPUT", "nothing written", "--secret-ref", "--secret-unstored", "--not-a-secret").Never(t, key)
		keyKept("(copied from show)")
	})

	w.Step("day1/git-history-never-holds-the-mask", func() {
		c.Git("add", "-A")
		c.TryGit("commit", "-q", "-m", "notes").OK(t)
		if log := c.Git("log", "-p", "--", ".lore/data/memories"); strings.Contains(log, "[redacted") || !strings.Contains(log, key) {
			t.Fatal("the committed row file holds a mask in place of the key")
		}
	})

	w.Step("day2/no-write-or-trash-output-prints-the-key", func() {
		// every command a person runs on the note after its key reads
		// masked: none may print the stored key (text or JSON)
		for _, args := range [][]string{
			{"memory", "edit", "--id", id, "--kind", "procedural"},
			{"memory", "edit", "--id", id, "--kind", "retrieved", "--meta", "team=ops"},
			{"memory", "archive", "--id", id},
			{"memory", "unarchive", "--id", id},
			{"memory", "show", "--id", id},
			{"row", "markdown", "--id", id},
			{"search", "--query", "deploy"},
			{"memory", "list"},
			{"sql", "--query", "select body from memories"},
		} {
			lap.LoreIn(c, args...).OK(t).Never(t, key)
			lap.LoreIn(c, append(args, "--json")...).OK(t).Never(t, key)
		}
		lap.LoreIn(c, "memory", "delete", "--id", id, "--yes", "--json").OK(t).Never(t, key)
		lap.LoreIn(c, "trash", "list").OK(t).Says(t, id).Never(t, key)
		lap.LoreIn(c, "trash", "list", "--json").OK(t).Never(t, key)
		lap.LoreIn(c, "trash", "show", "--id", id).OK(t).Says(t, "[redacted ").Never(t, key)
		lap.LoreIn(c, "trash", "show", "--id", id, "--json").OK(t).Never(t, key)
		// a restore screens the row again: the key no allow accepts is asked
		// about (never printed), and an answer brings the stored text back
		lap.LoreIn(c, "trash", "restore", "--id", id, "--yes", "--json").Refused(t, "E_NEEDS_CONFIRM", "--not-a-secret").Never(t, key)
		lap.LoreIn(c, "trash", "restore", "--id", id, "--not-a-secret", "--reason", "a fixture", "--yes", "--json").OK(t).Never(t, key)
		keyKept("(copied from show)")
	})
}
