//go:build journeys && !windows

package journeys

// sync_orgstore_once_test.go — an offline `lore sync` names each store once
// (devco K-7). Mira's own repo attaches her org's store; a write to it
// waits in the store clone. Offline, the sync of the attached store says
// "offline"; the org-store pass (plainfiles:4) used to try the same clone
// again and print a second line for it under its url's repo name
// ("store lore-team: … (offline …)"), a store `lore store list` never shows.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneySyncOfflineNamesTheOrgStoreOnce(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Stone", "mira@example.test")
	m := mira.Laptop()
	team := w.Remote("github.com/acme-co/lore-team")
	app := jPlainRepo(t, m, w.Remote("github.com/acme-co/app"), filepath.Join(m.Home, "code", "app"))

	w.Step("day1/org-store-attached-and-written", func() {
		m.LoreIn(app, "store", "init", "--name", "acme", "--url", team.SSHURL()).OK(t)
		m.LoreIn(app, "org", "create", "--org", "acme", "--store", team.SSHURL(), "--own", "github.com/acme-co/*", "--key", "human:mira@example.test", "--name", "Mira", "--yes").OK(t)
		m.LoreIn(app, "init").OK(t)
		m.LoreIn(app, "memory", "add", "--body", "invoices go out on the 1st", "--to", "acme").OK(t)
	})

	w.Step("day1/offline-sync-one-line-per-store", func() {
		m.Offline()
		r := m.LoreIn(app, "sync")
		out := r.Stdout + r.Stderr
		if strings.Contains(out, "store lore-team:") {
			t.Fatalf("the org store is printed twice, once under its url's repo name:\n%s", out)
		}
		if !strings.Contains(out, "store acme") {
			t.Fatalf("the attached org store has no line:\n%s", out)
		}
	})
}
