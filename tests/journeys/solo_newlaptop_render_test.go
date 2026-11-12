//go:build journeys && !windows

package journeys

// solo_newlaptop_render_test.go — the private render on a new laptop
// (B3-25). Sam keeps notes in a personal store and renders; on a second
// laptop a fresh clone has no private render yet. That is not a fault:
// doctor stays healthy and says "not rendered yet on this machine" as a
// note, and lore fix writes it.

import (
	"testing"
)

func TestJourneySoloNewLaptopRender(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	app := w.Remote("github.com/sam-dev/app")
	w.Remote(soloStore)

	w.Step("day1/notes-and-render", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, lap, soloName, soloEmail)
		soloSetupRest(t, lap, "create")
		lap.Lore("", "skill", "install", "--tool", "claude_code").OK(t)
		repo := lap.Clone(app, "app")
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "app: timestamps are UTC").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "I like short commit subjects", "--to", "me").OK(t)
		lap.LoreIn(repo, "render").OK(t)
		repo.CommitAll("lore: notes")
		repo.Git("push", "-q", "origin", "HEAD")
		lap.LoreIn(repo, "doctor").OK(t).Says(t, "Status: HEALTHY")
	})

	w.Step("day2/new-laptop-fresh-clone", func() {
		w.AdvanceDays(1)
		desk := sam.NewLaptop("desk")
		desk.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, desk, soloName, soloEmail)
		soloSetupRest(t, desk, "existing")
		desk.Lore("", "skill", "install", "--tool", "claude_code").OK(t)
		repo := desk.Clone(app, "app")
		// the first lore write (a personal note) wires git for lore's rows in the clone
		desk.LoreIn(repo, "memory", "add", "--body", "desk: first note", "--to", "me").OK(t)
		desk.LoreIn(repo, "doctor").OK(t).
			Says(t, "Status: HEALTHY", "not rendered yet on this machine").
			Never(t, "DEGRADED", "is missing:", "lines is")
		desk.LoreIn(repo, "fix", "--yes").OK(t).Says(t, "HEALTHY")
		desk.LoreIn(repo, "doctor").OK(t).Says(t, "Status: HEALTHY").Never(t, "not rendered yet on this machine")
	})
}
