//go:build journeys && !windows

package journeys

// migrate_teammate_scope_test.go — round 5 track M (devco:M-1, M-3, M-4).
// Ada's checkout of an old project carries a git-ignored lore.db a
// 0.1.18-dev build last wrote, with a cell stored as a BLOB: lore migrate
// run upgrades it (it failed with a bare E_INTERNAL). Ada commits the
// migration and pushes. Ben pulls, and his first write's .gitignore note
// says what the migration commit holds (it claimed the commit brings the
// lines). Ben's clone has no repo id (the old data had no repos row): a
// dir-scoped rule and decision mint the id every clone agrees on (they
// were refused with a `lore init` hint, and lore init said nothing to do).

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite" // the pure-Go driver lore ships, to build the old lore.db

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// oldDevDB writes the lore.db a 0.1.18-dev build left in c: a newer copy
// of the project's memory whose body is stored as a BLOB.
func oldDevDB(t testing.TB, c *teamharness.Clone) {
	t.Helper()
	db, err := sql.Open("sqlite", fileuri.URL(filepath.Join(c.Dir, ".lore", "lore.db"), ""))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // test fixture
	for _, q := range []string{
		`CREATE TABLE memories (id TEXT PRIMARY KEY, updated_at datetime NOT NULL, body TEXT NOT NULL)`,
		`INSERT INTO memories VALUES ('` + fixMemory + `', '2026-11-04 10:00:00.000000000 +0000 UTC', CAST('the widget cache warms at boot and at login' AS BLOB))`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestJourneyMigrateTeammateScope(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona("Ada Moreno", "ada@widgets.example").Laptop()
	ben := w.Persona("Ben Okafor", "ben@widgets.example").Laptop()
	remote := w.Remote("github.com/widgets/dock")
	var ca, cb *teamharness.Clone

	w.Step("day1/old-repo-with-a-dev-lore-db", func() {
		ca = ada.Clone(remote, "dock")
		fixOldRepo(t, ca)
		ca.WriteFile(".gitignore", ".lore/lore.db\n.lore/lore.db-shm\n.lore/lore.db-wal\n")
		ca.WriteFile("pkg/dock/README.md", "# dock\n")
		ca.CommitAll("dock package")
		ca.Git("push", "-q", "origin", "HEAD")
		oldDevDB(t, ca)
	})

	w.Step("day1/migrate-reads-the-blob-cell", func() {
		ada.LoreIn(ca, "migrate", "run").OK(t).Says(t, "git add .lore/data && git commit").Never(t, "E_INTERNAL")
		ca.Git("add", ".lore/data")
		ca.Git("commit", "-q", "-m", "lore: migrate rows to format 2")
		ca.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day2/teammate-first-write-names-what-the-commit-holds", func() {
		cb = ben.Clone(remote, "dock")
		ben.LoreIn(cb, "task", "add", "--title", "Dock keyboard pass").OK(t).
			Says(t, "migration commit holds only .lore/data", "`lore doctor --fix` adds them to .gitignore here instead; commit it").
			Never(t, "whose commit brings")
	})

	w.Step("day2/dir-scoped-writes-mint-the-repo-id", func() {
		ben.LoreIn(cb, "repo", "list").OK(t).Says(t, "(no repos)")
		ben.LoreIn(cb, "rule", "add", "--body", "Dock popouts restore focus to the opener.", "--summary", "Dock restores focus", "--scope", "dir:pkg/dock").OK(t).
			Never(t, "lore init", "no repo identity")
		ben.LoreIn(cb, "decision", "add", "--title", "Dock focus", "--body", "restore it on close", "--scope", "dir:pkg/dock").OK(t)
		ben.LoreIn(cb, "repo", "list").OK(t).Says(t, "rep_")
		ben.LoreIn(cb, "context", "--for-file", "pkg/dock/README.md").OK(t).Says(t, "Dock popouts restore focus")
	})
}
