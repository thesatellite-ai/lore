//go:build journeys && !windows

package journeys

// whoami_nocache_test.go — B-3: `lore whoami` is the first thing people
// run in a repo, and it asks only who they are. In a fresh clone it used to
// build the repo's whole row cache first (5 s on a 5,000-row repo). Now
// whoami (and `lore store list`, `lore setup list`) leave the cache empty;
// the first command that queries rows builds it and sees every note.

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // the cache's own driver, to count its rows read-only

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyWhoamiBuildsNoCache(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ana := w.Persona("Ana Reader", "ana@shop.example")
	remote := w.Remote("github.com/shop/notes")
	var desk *Machine
	var c *teamharness.Clone

	w.Step("day1/notes-pushed-from-the-laptop", func() {
		lap := ana.Laptop()
		lc := lap.Clone(remote, "notes")
		lap.LoreIn(lc, "init").OK(t)
		for _, body := range []string{"Refunds go through the gateway", "Dates are UTC", "Retry a 502 twice"} {
			lap.LoreIn(lc, "memory", "add", "--body", body).OK(t)
		}
		lc.CommitAll("notes")
		lc.Git("push", "-q")
	})

	w.Step("day2/a-second-machine-already-used-elsewhere", func() {
		// a brand-new machine's very first lore command records the
		// machine itself (a write, which opens everything once); B-3 is
		// the first command in a clone on a machine that used lore before
		desk = ana.NewLaptop("desk")
		other := desk.Clone(w.Remote("github.com/shop/site"), "site")
		desk.LoreIn(other, "init").OK(t)
		desk.LoreIn(other, "whoami").OK(t)
	})

	w.Step("day2/whoami-in-a-fresh-clone-reads-no-rows-into-its-cache", func() {
		c = desk.Clone(remote, "notes")
		desk.LoreIn(c, "whoami").OK(t).Says(t, "ana@shop.example")
		desk.LoreIn(c, "store", "list").OK(t)
		if n := repoCacheRows(t, desk.Home, c.Dir); n != 0 {
			t.Fatalf("whoami and store list put %d rows into the repo cache; want none (they read no rows)", n)
		}
	})

	w.Step("day2/the-first-list-builds-it-and-sees-every-note", func() {
		desk.LoreIn(c, "memory", "list").OK(t).Says(t, "Refunds go through the gateway", "Dates are UTC", "Retry a 502 twice")
		if n := repoCacheRows(t, desk.Home, c.Dir); n == 0 {
			t.Fatal("memory list left the repo cache empty; want it built")
		}
	})
}

// repoCacheRows counts the rows of every row table in the repo cache of
// checkout under home (tables starting with "_" are the cache's
// own bookkeeping and search index). No cache file counts as zero.
func repoCacheRows(t *testing.T, home, checkout string) int {
	t.Helper()
	dir := filepath.Join(home, ".lore", "cache", "repo")
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, e := range ents {
		// <repo folder name>-<short hash of its path>.db (sqlite.RepoCacheRel)
		if !strings.HasSuffix(e.Name(), ".db") || !strings.HasPrefix(e.Name(), filepath.Base(checkout)+"-") {
			continue
		}
		total += cacheFileRows(t, filepath.Join(dir, e.Name()))
	}
	return total
}

// cacheFileRows counts the rows of one cache file's row tables.
func cacheFileRows(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE '\_%' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, n := range names {
		var c int
		if err := db.QueryRow(`SELECT count(*) FROM "` + strings.ReplaceAll(n, `"`, `""`) + `"`).Scan(&c); err != nil {
			t.Fatal(err)
		}
		total += c
	}
	return total
}
