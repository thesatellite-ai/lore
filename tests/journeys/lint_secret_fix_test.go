//go:build journeys && !windows

package journeys

// lint_secret_fix_test.go — devco:D-752 (bug hunt H-11): lore lint's
// secret_found fix line told the person to run `lore edit mem_…`, the id by
// position, although lore edit takes --id (named flags everywhere). Rae's row
// holds a token an older lore (or a hand edit) wrote; lore lint names it, its
// fix line spells `lore edit --id <id>`, and that command runs exactly as
// printed (EDITOR=true: the editor closes with no change, nothing written).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// jLintFixEdit is the edit command in lint's fix line, flag and id.
var jLintFixEdit = regexp.MustCompile(`lore edit --id ((?:mem|tsk|rul|dec)_[0-9a-f]+)`)

// jLintFixPositional is the old shape: the id right after `lore edit`.
var jLintFixPositional = regexp.MustCompile(`lore edit (?:mem|tsk|rul|dec)_`)

func TestJourneyLintSecretFixLineNamed(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lap := w.Persona("Rae Lint", "rae@example.test").Laptop()
	repo := lap.Clone(w.Remote("github.com/acme-co/lint-fix"), "lint-fix")
	const note = "placeholder note for the lint fix line"
	// a made-up token, built at run time so the source holds none
	token := "ghp_" + strings.Repeat("Zq7Lm2Xv9R", 4)[:36]

	w.Step("day1/a-row-holds-a-token", func() {
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", note).OK(t)
		var file string
		_ = filepath.WalkDir(filepath.Join(repo.Dir, ".lore", "data"), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || file != "" {
				return err
			}
			if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), note) {
				file = p
			}
			return nil
		})
		if file == "" {
			t.Fatalf("no row file under %s holds %q", repo.Dir, note)
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(strings.Replace(string(b), note, "token "+token, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	})

	w.Step("day1/lint-prints-a-named-edit-that-runs", func() {
		out := lap.LoreIn(repo, "lint").Says(t, "secret_found").Out()
		if jLintFixPositional.MatchString(out) {
			t.Fatalf("lore lint printed `lore edit <id>` with the id by position (lore edit takes --id):\n%s", out)
		}
		m := jLintFixEdit.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("lore lint's fix line names no `lore edit --id <id>`:\n%s", out)
		}
		lap.SetEnv("EDITOR", "true")
		defer lap.SetEnv("EDITOR", "")
		lap.LoreIn(repo, "edit", "--id", m[1]).OK(t).Says(t, "no change", "nothing written")
	})
}
