//go:build journeys && !windows

package journeys

// bigfolder_test.go — a personal store whose feeds folder grows to
// hundreds of rows through hand commits in the store clone (an import
// script, a pull from another laptop). Before devco D-790 every lore call
// on such a store read every row file at HEAD twice over (the actors
// lookup of each command ignored its table hint in a store clone, and
// `lore sql` filled its trash table from every row even for `select 1`),
// and gitfast parsed the folder's tree once per file: ~4 s per call at
// 5,000 rows. The reads are now narrowed; this journey keeps them right:
//
//   - rows a hand commit adds are counted and found by the next read;
//   - a hand edit of one row is what the next search finds;
//   - a row moved to the trash is listed by `select … from trash`, and a
//     query that does not name the trash still answers.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyBigFolderReadsStayRight(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona("Sam Okoro", "sam@acme.example")
	lap := sam.Laptop()
	personal := w.Remote("github.com/sam-okoro/notes")
	app := lap.Clone(w.Remote("github.com/acme-co/app"), "app")
	const extra = 400
	var seed, seedFile string
	var store *teamharness.Clone

	w.Step("day1/feeds-folder-gets-its-first-row", func() {
		lap.LoreIn(app, "store", "init", "--name", "me", "--url", personal.SSHURL(), "--yes").OK(t)
		out := lap.LoreIn(app, "memory", "add", "--to", "me/feeds", "--create", "--body", "seed event orbit", "--yes").OK(t)
		seed = regexp.MustCompile(`mem_[0-9a-f]{32}`).FindString(out.Out())
		if seed == "" {
			t.Fatalf("no memory id in: %s", out.Out())
		}
		seedFile = strings.TrimSpace(lap.LoreIn(app, "path", "--id", seed).OK(t).Stdout)
		if !strings.HasSuffix(seedFile, seed+".json") {
			t.Fatalf("lore path --id %s printed %q", seed, seedFile)
		}
		store = &teamharness.Clone{A: lap.Actor, Dir: filepath.Dir(filepath.Dir(filepath.Dir(seedFile)))}
	})

	w.Step("day2/an-import-commits-many-rows-by-hand", func() {
		doc, err := os.ReadFile(seedFile)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Dir(seedFile)
		for i := range extra {
			id := fmt.Sprintf("%s%012x", seed[:len(seed)-12], 0x100000+i) // same prefix and version bits, new tail
			body := strings.ReplaceAll(string(doc), seed, id)
			body = strings.Replace(body, "seed event orbit", fmt.Sprintf("imported event %d orbit", i), 1)
			if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		store.Git("add", "-A")
		store.Git("commit", "-q", "-m", "import feed events")
		lap.LoreIn(app, "sql", "--store", "me", "--query", "select count(*) as n from memories_all where source = 'me/feeds'").
			OK(t).Says(t, fmt.Sprint(extra+1))
	})

	w.Step("day3/a-hand-edit-is-what-the-next-search-finds", func() {
		doc, err := os.ReadFile(seedFile)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(string(doc), "seed event orbit", "seed event nebula", 1)
		if edited == string(doc) {
			t.Fatalf("the seed row file does not hold its body: %s", doc)
		}
		if err := os.WriteFile(seedFile, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		store.Git("commit", "-q", "-am", "fix a typo by hand")
		lap.LoreIn(app, "search", "--store", "me", "--query", "nebula").OK(t).Says(t, seed)
	})

	w.Step("day4/trash-reads-only-when-asked-and-stays-right", func() {
		lap.LoreIn(app, "memory", "delete", "--id", seed, "--yes").OK(t)
		lap.LoreIn(app, "sql", "--store", "me", "--query", "select 1 as one").OK(t).Says(t, "one")
		lap.LoreIn(app, "sql", "--store", "me", "--query", "select id from trash").OK(t).Says(t, seed)
		lap.LoreIn(app, "sql", "--store", "me", "--query", "select count(*) as n from memories_all where source = 'me/feeds'").
			OK(t).Says(t, fmt.Sprint(extra))
	})
}
