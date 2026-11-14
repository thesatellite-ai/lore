//go:build journeys && !windows

package journeys

// neworg_walk_test.go — a newcomer starts an org with three stores from one
// code repo, following the docs and the messages only (plainfiles:4):
//
//   - `lore org create` on a brand-new empty repo used to answer "store
//     branch does not exist: … has no branch main" with a hint about the
//     repo's other branches; it now says the repo is empty and names
//     `lore store init`, the step the docs give;
//   - a note written with --to <second org store>/<folder> said "lore sync
//     sends it", and `lore sync` synced only the repo's attached store, so
//     the commits never left the laptop; `lore sync` now pushes every org
//     store clone that holds waiting commits.

import "testing"

func TestJourneyNewOrgThreeStores(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Castell", "mira@acme.example")
	lap := mira.Laptop()
	main := w.Remote("github.com/acme-co/org-main")
	clients := w.Remote("github.com/acme-co/org-clients")
	research := w.Remote("github.com/acme-co/org-research")
	app := lap.Clone(w.Remote("github.com/acme-co/app"), "app")
	create := []string{"org", "create", "--org", "acme", "--store", main.SSHURL(), "--key", "human:mira@acme.example", "--name", "Mira", "--yes"}

	w.Step("day1/org-create-on-an-empty-repo-names-store-init", func() {
		lap.LoreIn(app, create...).Refused(t, "is empty", "lore store init --name <name> --url <url>")
	})

	w.Step("day1/org-with-three-stores", func() {
		lap.LoreIn(app, "store", "init", "--name", "acme", "--url", main.SSHURL()).OK(t)
		lap.LoreIn(app, create...).OK(t).Says(t, "added org entry acme")
		lap.LoreIn(app, "init", "--non-interactive", "--name=app").OK(t)
		lap.LoreIn(app, "org", "store", "add", "--org", "acme", "--url", clients.SSHURL(), "--yes").OK(t)
		lap.LoreIn(app, "org", "store", "add", "--org", "acme", "--url", research.SSHURL(), "--yes").OK(t)
		lap.LoreIn(app, "store", "folder", "add", "--store", "org-clients", "--folder", "north").OK(t)
		lap.LoreIn(app, "memory", "add", "--body", "Clients get invoices on the 1st", "--to", "org-clients/north").OK(t).
			Says(t, "lore sync sends it")
	})

	w.Step("day1/sync-sends-the-second-store", func() {
		if clients.Has("refs/heads/main") {
			t.Fatal("org-clients reached its remote before any sync: the test proves nothing")
		}
		lap.LoreIn(app, "sync").OK(t).Says(t, "store org-clients")
		if !clients.Has("refs/heads/main") {
			t.Fatal("lore sync left the org-clients commits on the laptop (the write said lore sync sends it)")
		}
		lap.LoreIn(app, "sync").OK(t).Never(t, "store org-clients")
	})
}
