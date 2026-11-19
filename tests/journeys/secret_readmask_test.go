//go:build journeys && !windows

package journeys

// secret_readmask_test.go — secrets that reach rows without the write
// scan (devco:R-10, devco:R-14). A token in a git branch name was
// recorded in every row made on that branch (`_o.git.branch`) and pushed
// with it; a hand-made row file committed with the hooks off was printed
// raw by show, search and memory list and rendered into agent context.
// Now the branch is recorded as "[redacted branch]", and a stored match
// reads redacted everywhere, stays out of render, and render names the
// row; `lore check leaks --rows` still reports it with one hash form.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneySecretsThatSkipTheWriteScan(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dev := w.Persona("Dev One", "dev.one@example.test")
	lap := dev.Laptop()
	tok := "ghp_" + strings.Repeat("Zq4", 12) // built at run time: no token-shaped literal in the repo
	var c *teamharness.Clone

	w.Step("day1/a-repo-with-lore", func() {
		c = lap.Clone(w.Remote("github.com/widgets/masks"), "masks")
		lap.LoreIn(c, "init").OK(t)
		c.WriteFile("README.md", "# widgets\n")
		c.Git("add", "-A")
		c.TryGit("commit", "-q", "-m", "start").OK(t)
	})

	w.Step("day1/a-row-made-on-a-branch-named-with-a-token", func() {
		c.Git("checkout", "-q", "-b", "feat/"+tok)
		lap.LoreIn(c, "memory", "add", "--body", "made on the branch").OK(t).Never(t, tok)
		if got := c.Git("log", "--all", "-p"); strings.Contains(got, tok) {
			t.Fatal("a commit holds the branch token")
		}
		_ = filepath.WalkDir(filepath.Join(c.Dir, ".lore"), func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), tok) {
					t.Errorf("%s holds the branch token", p)
				}
			}
			return nil
		})
		lap.LoreIn(c, "check", "leaks", "--rows").OK(t)
		c.Git("checkout", "-q", "-")
	})

	var id string
	w.Step("day2/a-hand-made-row-holds-a-token", func() {
		lap.LoreIn(c, "memory", "add", "--body", "HANDROW plain").OK(t)
		matches, _ := filepath.Glob(filepath.Join(c.Dir, ".lore", "data", "memories", "*.json"))
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err == nil && strings.Contains(string(b), "HANDROW plain") {
				id = strings.TrimSuffix(filepath.Base(m), ".json")
				if err := os.WriteFile(m, []byte(strings.Replace(string(b), "HANDROW plain", "HANDROW key "+tok, 1)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		if id == "" {
			t.Fatal("the memory row file was not found")
		}
	})

	w.Step("day2/every-read-shows-it-redacted", func() {
		lap.LoreIn(c, "show", id).OK(t).Says(t, "[redacted github_token]").Never(t, tok)
		lap.LoreIn(c, "search", "HANDROW").OK(t).Says(t, "[redacted github_token]").Never(t, tok)
		lap.LoreIn(c, "memory", "list").OK(t).Says(t, "[redacted github_token]").Never(t, tok)
		lap.LoreIn(c, "show", id, "--json").OK(t).Never(t, tok)
	})

	w.Step("day2/render-leaves-it-out-and-names-it", func() {
		lap.LoreIn(c, "render").OK(t).Says(t, id, "left out of render").Never(t, tok)
		_ = filepath.WalkDir(c.Dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
				if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), tok) {
					t.Errorf("%s holds the token", p)
				}
			}
			return nil
		})
	})

	w.Step("day2/check-leaks-rows-gives-one-hash-form", func() {
		r := lap.LoreIn(c, "check", "leaks", "--rows").Refused(t, "E_LEAK_BLOCKED", id).Never(t, tok)
		out := r.Out()
		i := strings.Index(out, "sha256:")
		if i < 0 {
			t.Fatalf("no fingerprint:\n%s", out)
		}
		fp := out[i : i+len("sha256:")+12]
		if !strings.Contains(out, "--match-hash "+fp) {
			t.Fatalf("the allow line does not give the shown fingerprint %s:\n%s", fp, out)
		}
	})
}
