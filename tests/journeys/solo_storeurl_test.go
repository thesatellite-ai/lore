//go:build journeys && !windows

package journeys

// solo_storeurl_test.go — the solo dev's personal store repo is renamed
// while a note waits to be pushed, and a new laptop restores a repo that
// was set up with a local-path remote (bug hunt B3-15, B3-16):
//
//	day 1    Sam's personal store lore-me holds a note; he renames the repo
//	         on GitHub to lore-personal before his next note is pushed
//	day 1    `lore store set-url me <new>` carries the clone holding the
//	         waiting note: the next sync pushes it to lore-personal, the
//	         note stays in every list, doctor is quiet
//	day 2    a repo first set up with a local-path remote is cloned from
//	         GitHub on a new laptop: its first sync records that address
//	         silently (no "remote changed", no fork offer)
//
// Named TestJourneySolo… so the solo persona's gate step runs it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Placeholders of this journey.
const (
	jsuName     = "Sam Solo"
	jsuEmail    = "sam@solo.example"
	jsuRepo     = "github.com/sam-dev/site"
	jsuOldStore = "github.com/sam-dev/lore-me"
	jsuNewStore = "github.com/sam-dev/lore-personal"
)

// jsuRemoteHas reports whether the remote's main holds a memory row
// containing body.
func jsuRemoteHas(w *World, slug, body string) bool {
	r := w.Remote(slug)
	for _, f := range r.FilesMatching("main", "memories/") {
		if s, ok := r.Read("main", f); ok && strings.Contains(s, body) {
			return true
		}
	}
	return false
}

func TestJourneySoloStoreRenamedWithWaitingNote(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(jsuName, jsuEmail)
	m := sam.Laptop()
	old := w.Remote(jsuOldStore)
	app := m.Clone(w.Remote(jsuRepo), "site")

	w.Step("day1/personal-store-and-a-first-note", func() {
		m.LoreIn(app, "init").OK(t)
		m.LoreIn(app, "store", "init", "me", old.SSHURL(), "--yes").OK(t)
		m.LoreIn(app, "memory", "add", "--body", "pushed before the rename", "--to", "me").OK(t)
		if !jsuRemoteHas(w, jsuOldStore, "pushed before the rename") {
			t.Fatalf("the first note did not reach lore-me")
		}
	})

	w.Step("day1/repo-renamed-then-a-note-waits", func() {
		// Sam renames the repo on GitHub: the old address is gone.
		if err := os.Rename(old.Dir, filepath.Join(filepath.Dir(old.Dir), filepath.Base(jsuNewStore)+".git")); err != nil {
			t.Fatal(err)
		}
		m.LoreIn(app, "memory", "add", "--body", "written after the rename", "--to", "me").OK(t)
	})

	w.Step("day1/set-url-carries-the-waiting-note", func() {
		nw := w.Remote(jsuNewStore)
		r := m.LoreIn(app, "store", "set-url", "me", nw.SSHURL()).OK(t)
		r.Says(t, "is now at "+nw.SSHURL(), "1 unpushed commit(s)", "`lore sync` sends its waiting commits")
		m.LoreIn(app, "sync").OK(t).Says(t, "store me: sent 1")
		if !jsuRemoteHas(w, jsuNewStore, "written after the rename") {
			t.Errorf("the note written while the old address was gone never reached lore-personal")
		}
		list := m.LoreIn(app, "memory", "list").OK(t)
		list.Says(t, "pushed before the rename", "written after the rename")
		m.LoreIn(app, "doctor").Never(t, "store_clone")
		// one clone of the store on this machine, now pointing at the new address
		var clones []string
		_ = filepath.WalkDir(m.LorePath("stores"), func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() && d.Name() == ".git" {
				clones = append(clones, filepath.Dir(p))
				return filepath.SkipDir
			}
			return nil
		})
		if len(clones) != 1 {
			t.Errorf("want one clone of the personal store, got %v", clones)
		}
	})
}

func TestJourneySoloNewLaptopFirstAddress(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(jsuName, jsuEmail)
	m := sam.Laptop()
	repo := w.Remote(jsuRepo)
	// On the first laptop the repo's remote is a local path (no address).
	local := m.CloneURL(repo, repo.Dir, "site")

	w.Step("day1/repo-set-up-with-a-local-remote", func() {
		m.LoreIn(local, "init").OK(t)
		m.LoreIn(local, "memory", "add", "--body", "the repo's first note").OK(t)
		m.LoreIn(local, "sync").OK(t).Never(t, "remote changed")
		local.CommitAll("lore")
		local.AdminPush("origin", "HEAD:main")
	})

	w.AdvanceDays(1)
	w.Step("day2/new-laptop-first-sync-is-quiet", func() {
		desk := sam.NewLaptop("desk")
		site := desk.Clone(repo, "site")
		r := desk.LoreIn(site, "sync").OK(t)
		r.Never(t, "remote changed", "lore repo fork", "no confirmed address")
		desk.LoreIn(site, "sync").OK(t).Never(t, "remote changed")
		desk.LoreIn(site, "doctor").Never(t, "unconfirmed")
		meta, err := os.ReadFile(filepath.Join(site.Dir, ".lore", "data", "_meta.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(meta), "github.com/sam-dev/site") {
			t.Errorf("_meta.json does not record the clone's address as confirmed:\n%s", meta)
		}
		desk.LoreIn(site, "memory", "list").OK(t).Says(t, "the repo's first note")
	})
}
