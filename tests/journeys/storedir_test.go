//go:build journeys && !windows

package journeys

// storedir_test.go — the upgrader, part four: a store of the first plain
// layout (lore_store: 3) moves its own rows into _store/ (D-ad). Sam ran
// the build that first laid stores out as plain folders: his store clone
// sits at ~/.lore/stores/<owner>/<repo> with the store's own rows (the
// root folder's) as <table>/ folders at the store's top, right beside his
// project folder acme/, and lore's README of that build. He installs this
// lore and runs one command.
//
// What he checks: lore says the store's own rows moved into _store/; the
// remote gets exactly one commit "lore: store layout — the store's own
// rows move to _store/" with lore_store: 4 and the current README; no
// table folder is left at the store's top; a root row he had edited by
// hand and not committed moves with it and is picked up as his hand edit;
// his notes read at the same ids; the next command is quiet.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// sdSubject is the layout commit's title.
const sdSubject = "lore: store layout — the store's own rows move to _store/"

// sdPrjID finds the project id in a folder's _meta.json.
var sdPrjID = regexp.MustCompile(`prj_[0-9a-f]{32}`)

func TestJourneyUpgraderStoreDir(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona("Sam Flat", "sam.flat@example.test")
	m := sam.Laptop()
	remote := w.Remote("github.com/acme/lore-flat")
	dir := m.LorePath("stores", "acme", "lore-flat")
	scratch := m.Clone(w.Remote("github.com/sam/scratch"), "scratch")
	bodies := map[string]string{}
	var rootIDs, acmeIDs []string

	w.Step("day1/install-over-the-first-plain-layout", func() {
		scratch.Lore("init", "--non-interactive", "--name=scratch").OK(t)
		rows := map[string]string{}
		for i := range 3 {
			body := "flat layout note " + string(rune('a'+i))
			id := olMemID.FindString(scratch.Lore("memory", "add", "--body", body).OK(t).Out())
			b, ok := scratch.ReadFile(filepath.Join(".lore", "data", "memories", id+".json"))
			if id == "" || !ok {
				t.Fatalf("no row file for note %d", i)
			}
			rows[id], bodies[id] = b, body
		}
		for id := range rows {
			if len(rootIDs) < 2 {
				rootIDs = append(rootIDs, id)
			} else {
				acmeIDs = append(acmeIDs, id)
			}
		}
		meta, ok := scratch.ReadFile(filepath.Join(".lore", "data", "_meta.json"))
		if !ok {
			t.Fatal("the scratch repo has no _meta.json")
		}
		earlier, err := os.ReadFile(filepath.Join("..", "..", "adapters", "backend", "rowfs", "testdata", "readme-earlier.txt"))
		if err != nil {
			t.Fatal(err)
		}
		sid, err := ids.New[ids.Store]()
		if err != nil {
			t.Fatal(err)
		}
		seed := filepath.Join(w.Root, "seed-lore-flat")
		w.Git(w.Root, "init", "-q", "-b", "main", seed)
		olWrite(t, filepath.Join(seed, "lore-store.yaml"), "lore_store: 3\nid: "+sid.String()+"\nvisibility: internal\ncreated: \"2026-11-12\"\n")
		olWrite(t, filepath.Join(seed, "README.md"), string(earlier))
		olWrite(t, filepath.Join(seed, "_meta.json"), meta)
		for _, id := range rootIDs {
			olWrite(t, filepath.Join(seed, "memories", id+".json"), rows[id])
		}
		for _, id := range acmeIDs {
			olWrite(t, filepath.Join(seed, "acme", "memories", id+".json"), rows[id])
		}
		pid, err := ids.New[ids.Project]()
		if err != nil {
			t.Fatal(err)
		}
		olWrite(t, filepath.Join(seed, "acme", "_meta.json"), sdPrjID.ReplaceAllString(meta, pid.String()))
		w.Git(seed, "add", "-A")
		w.Git(seed, "-c", "user.name=First Build", "-c", "user.email=build@example.test", "commit", "-q", "-m", "lore: first plain layout")
		w.Git(seed, "push", "-q", remote.Dir, "main")

		// Sam's clone as that build left it, with one root row edited by
		// hand and not committed
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			t.Fatal(err)
		}
		w.Git(m.Home, "clone", "-q", remote.Dir, dir)
		w.Git(dir, "remote", "set-url", "origin", "git@github.com:acme/lore-flat.git")
		edited := strings.Replace(rows[rootIDs[0]], bodies[rootIDs[0]], "flat layout note, edited by hand", 1)
		olWrite(t, filepath.Join(dir, "memories", rootIDs[0]+".json"), edited)
		bodies[rootIDs[0]] = "flat layout note, edited by hand"

		r := m.Lore(m.Home, "doctor")
		r.Says(t, "store lore-flat: layout commit ", " rows moved to _store/")
		// C5: the layout commit reaches the store with the person's sync, run
		// in the store's clone (the home folder is in no setup)
		m.Lore(dir, "sync").OK(t).Says(t, "pushed store lore-flat")
	})

	w.Fact("the remote has one layout commit, _store/ rows, lore_store 4, the current README, no table folder at the top", func(c Ctx) {
		files := remote.Files("main")
		all := "\n" + strings.Join(files, "\n")
		for _, id := range rootIDs {
			if !strings.Contains(all, "\n_store/memories/"+id+".json") {
				c.Errorf("_store/memories/%s.json is not on the remote:%s", id, all)
			}
		}
		for _, id := range acmeIDs {
			if !strings.Contains(all, "\nacme/memories/"+id+".json") {
				c.Errorf("acme/memories/%s.json moved:%s", id, all)
			}
		}
		if strings.Contains(all, "\nmemories/") || strings.Contains(all, "\n_meta.json") {
			c.Errorf("root rows still at the store's top:%s", all)
		}
		if n := strings.Count(strings.Join(remote.Log("main"), "\n"), sdSubject); n != 1 {
			c.Errorf("%d layout commits on the remote, want 1", n)
		}
		if marker, _ := remote.Read("main", "lore-store.yaml"); !strings.Contains(marker, "lore_store: 4") {
			c.Errorf("marker %q, want lore_store: 4", marker)
		}
		if readme, _ := remote.Read("main", "README.md"); !strings.Contains(readme, "`_store/`") {
			c.Errorf("the earlier README was not replaced:\n%s", readme)
		}
	})

	w.Step("day1/notes-read-and-the-hand-edit-is-his", func() {
		for _, id := range rootIDs {
			m.Lore(dir, "show", id).OK(t).Says(t, bodies[id])
		}
		for _, id := range acmeIDs {
			m.Lore(filepath.Join(dir, "acme"), "show", id).OK(t).Says(t, bodies[id])
		}
		m.Lore(dir, "memory", "add", "--body", "a note after the move").OK(t) // a write: what waits is pushed
		if got, _ := remote.Read("main", "_store/memories/"+rootIDs[0]+".json"); !strings.Contains(got, "edited by hand") {
			t.Errorf("the hand edit did not reach the remote: %s", got)
		}
		if subj := strings.Join(remote.Log("main"), "\n"); !strings.Contains(subj, "hand edit: memories "+rootIDs[0]) {
			t.Errorf("no hand edit commit for the moved row:\n%s", subj)
		}
	})

	w.Step("day2/the-next-command-is-quiet", func() {
		w.AdvanceDays(1)
		m.Lore(m.Home, "doctor").Never(t, "_store/", "rows moved", "the store's own rows moved")
	})
}
