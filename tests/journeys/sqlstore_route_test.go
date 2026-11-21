//go:build journeys && !windows

package journeys

// sqlstore_route_test.go — `lore sql run --store <store>` in a repo reads
// that store alone (devco D-799, owner P-16). A repo set up with lore
// attaches a store of its own (`lore store init --name crew`, recorded in
// .lore/stores.yaml: neither an org nor `me`). Before: `--store crew` ran
// on the repo's lens, so `SELECT name FROM res_person` answered with the
// repo's person, res_person_all mixed the repo's rows in, and a name no
// store answers to (`--store nope`) ran on the repo with no error.
//
//	repo (carol_repo) + crew (bob_crew, the kind `person`)
//	  --store crew ─▶ bob_crew only
//	  --store repo ─▶ carol_repo only (the kind from crew, phone masked)
//	  --store nope ─▶ E_NOT_FOUND

import (
	"path/filepath"
	"testing"
)

// TestJourneySoloSQLStoreRoute runs in the solo gate step (^TestJourneySolo).
func TestJourneySoloSQLStoreRoute(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona("Ada Quill", "ada@crew.example")
	lap := ada.Laptop()
	bare := filepath.Join(w.Root, "srv", "git", "crew-notes.git")
	w.Git(w.Root, "init", "-q", "--bare", "-b", "main", bare)
	code := jPlainRepo(t, lap, w.Remote("github.com/adaquill/app"), filepath.Join(lap.Home, "work", "app"))

	w.Step("day1/a-repo-with-its-own-store", func() {
		lap.LoreIn(code, "init", "--non-interactive", "--name=app").OK(t)
		lap.LoreIn(code, "store", "init", "--name", "crew", "--url", bare, "--yes").OK(t)
		lap.LoreIn(code, "resource", "kind", "add", "--kind", "person", "--to", "crew", "--yes").OK(t)
		lap.LoreIn(code, "resource", "field", "add", "--kind", "person", "--field", "phone", "--type", "text", "--sensitive", "--to", "crew", "--yes").OK(t)
		lap.LoreIn(code, "resource", "add", "--kind", "person", "--name", "bob_crew", "--set", "phone=555-0101", "--to", "crew", "--yes").OK(t)
		lap.LoreIn(code, "resource", "add", "--kind", "person", "--name", "carol_repo", "--set", "phone=555-0202", "--to", "repo", "--yes").OK(t)
	})

	w.Step("day2/sql-store-reads-that-store-alone", func() {
		w.AdvanceDays(1)
		lap.LoreIn(code, "sql", "run", "--store", "crew", "--query", "SELECT name FROM res_person").OK(t).
			Says(t, "bob_crew", "(1 row)").Never(t, "carol_repo")
		lap.LoreIn(code, "sql", "run", "--store", "crew", "--query", "SELECT source, name FROM res_person_all").OK(t).
			Says(t, "bob_crew", "(1 row)").Never(t, "carol_repo")
		lap.LoreIn(code, "sql", "run", "--store", "repo", "--query", "SELECT name, phone FROM res_person").OK(t).
			Says(t, "carol_repo", "(1 row)").Never(t, "bob_crew", "555-0202")
		lap.LoreIn(code, "sql", "run", "--store", "repo", "--query", "SELECT source, name FROM res__all").OK(t).
			Says(t, "carol_repo", "(1 row)").Never(t, "bob_crew")
		lap.LoreIn(code, "sql", "run", "--store", "nope", "--query", "SELECT name FROM res_person").
			Refused(t, "E_NOT_FOUND", "nope").Never(t, "carol_repo")
		// no --store: every attached store, as before
		lap.LoreIn(code, "sql", "run", "--query", "SELECT source, name FROM res_person_all ORDER BY name").OK(t).
			Says(t, "bob_crew", "carol_repo", "(2 rows)")
	})
}
