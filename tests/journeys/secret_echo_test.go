//go:build journeys && !windows

package journeys

// secret_echo_test.go — a token pasted into the wrong place (devco R-11,
// R-17): refusals that repeat what was typed show the rule and a hash,
// never the token, and a setup answer that looks like one is refused
// before anything is saved.

import (
	"os"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// journeyFakeToken is a synthetic GitHub token, built at run time.
func journeyFakeToken() string { return "gh" + "p_" + "aB3dE9fG1hJ2kL8mN4pQ7rS5tU6vW0xY1zC2" }

func TestJourneySecretNeverEchoed(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	pat := w.Persona("Pat Ostro", "pat@echo.example")
	lap := pat.Laptop()
	app := w.Remote("github.com/echo-dev/app")
	tok := journeyFakeToken()
	var repo *teamharness.Clone

	w.Step("day1/token-in-the-wrong-argument", func() {
		repo = lap.Clone(app, "app")
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "show", tok).Fails(t, 20, "[redacted github_token sha256:").Never(t, tok)
		lap.LoreIn(repo, "show", tok, "--json").Never(t, tok)
		lap.LoreIn(repo, "memory", "add", "--to", tok, "--body", "x").Never(t, tok)
		lap.LoreIn(repo, "config", "set", "--key", tok, "--value", "1").Never(t, tok)
		lap.LoreIn(repo, "sql", "--query", "SELECT '"+tok+"' AS x").OK(t).Says(t, "[redacted github_token").Never(t, tok)
	})

	w.Step("day1/token-in-a-setup-answer", func() {
		lap.Lore("", "setup", "apply", "--setup", "tok", "--yes", "--step", "kind", "--answers", `{"kind":"solo","name":"tok"}`).OK(t)
		lap.Lore("", "setup", "apply", "--setup", "tok", "--yes", "--step", "identity", "--answers", `{"name":"M `+tok+`","email":"m@echo.example","handle":"m"}`).
			Fails(t, 20, "looks like a secret", "edit that answer").Never(t, tok, "--secret-unstored", "--not-a-secret")
		cfg, _ := os.ReadFile(lap.LorePath("config.yaml"))
		if strings.Contains(string(cfg), tok) {
			t.Fatal("config.yaml holds the refused answer")
		}
		lap.Lore("", "setup", "show", "--name", "tok").Never(t, tok)
	})
}
