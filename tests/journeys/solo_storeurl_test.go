//go:build journeys && !windows

package journeys

// solo_storeurl_test.go — the solo dev's personal store repo is renamed
// while a note waits to be pushed, and a new laptop restores a repo that
// was set up with a local-path remote (bug hunt B3-15, B3-16):
//
//	day 1    Sam's personal store lore-me holds a note; he renames the repo
//	         on GitHub to lore-personal before his next note is pushed
//	day 1    `lore store set-url --name me --url <new>` carries the clone holding the
//	         waiting note: the next sync pushes it to lore-personal, the
//	         note stays in every list, doctor is quiet
//	day 2    a repo first set up with a local-path remote is cloned from
//	         GitHub on a new laptop: a sync with nothing to commit writes
//	         nothing (I-7); the first sync after a note records that
//	         address silently (no "remote changed", no fork offer)
//
// Named TestJourneySolo… so the solo persona's gate step runs it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
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
		m.LoreIn(app, "sync").OK(t) // C5: the note reaches lore-me with Sam's sync
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
		desk.LoreIn(site, "sync").OK(t).Never(t, "remote changed", "lore repo fork", "no confirmed address")
		if st := site.Status(); st != "" {
			t.Fatalf("a sync with nothing to commit changed files:\n%s", st)
		}
		desk.LoreIn(site, "memory", "add", "--body", "a note from the desk").OK(t)
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

// TestJourneySoloStoreRemadeEmpty (B3-29): Sam deletes his kb store's
// repo and makes an empty one at the same address to start fresh. Lore
// never fills it with the old store on its own: the write and the sync
// say the remote was reset and name both ways out; he picks "start
// fresh" (store forget --yes, then store init), and only the new note is
// in the new store.
func TestJourneySoloStoreRemadeEmpty(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(jsuName, jsuEmail)
	m := sam.Laptop()
	kb := w.Remote("github.com/sam-dev/kb")
	app := m.Clone(w.Remote(jsuRepo), "site")
	remake := func() {
		t.Helper()
		if err := os.RemoveAll(kb.Dir); err != nil {
			t.Fatal(err)
		}
		w.Git(w.Root, "init", "-q", "--bare", "-b", "main", kb.Dir)
	}

	w.Step("day1/kb-with-an-old-note", func() {
		m.LoreIn(app, "init").OK(t)
		m.LoreIn(app, "store", "init", "kb", kb.SSHURL(), "--yes").OK(t)
		m.LoreIn(app, "memory", "add", "--body", "an old note", "--to", "kb").OK(t)
		m.LoreIn(app, "sync").OK(t) // C5: the note reaches kb with Sam's sync
		app.CommitAll("lore")
		app.Git("push", "-q", "origin", "HEAD:main")
	})

	// B2-34: Sam's second laptop holds a clone of the old kb store too
	var desk *Machine
	var deskApp *teamharness.Clone
	w.Step("day1/desk-clones-kb", func() {
		desk = sam.NewLaptop("desk")
		deskApp = desk.Clone(w.Remote(jsuRepo), "site")
		desk.LoreIn(deskApp, "sync").OK(t)
		desk.LoreIn(deskApp, "memory", "list").OK(t).Says(t, "an old note")
	})

	w.Step("day1/repo-made-again-empty", func() {
		remake()
		// C5: the write commits locally; lore sync finds the reset store and
		// refuses, naming both ways out
		m.LoreIn(app, "memory", "add", "--body", "written after the reset", "--to", "kb").OK(t).
			Says(t, "committed locally to store kb")
		r := m.LoreIn(app, "sync")
		if r.Code == 0 {
			t.Errorf("sync into a reset store must fail naming it: %s", r)
		}
		r.Says(t, "was reset", "lore store push --restore", "lore store forget")
		if refs := w.TryGit(kb.Dir, "for-each-ref").Stdout; strings.TrimSpace(refs) != "" {
			t.Errorf("lore pushed the old store into the new empty repo: %s", refs)
		}
	})

	w.Step("day1/start-fresh", func() {
		m.LoreIn(app, "store", "remove", "kb", "--yes").OK(t)
		if r := m.LoreIn(app, "store", "forget", kb.SSHURL()); r.Code == 0 {
			t.Errorf("forget of a clone holding the old store must ask first: %s", r)
		}
		m.LoreIn(app, "store", "forget", kb.SSHURL(), "--yes").OK(t)
		m.LoreIn(app, "store", "init", "kb", kb.SSHURL(), "--yes").OK(t).Says(t, "created store kb").Never(t, "already a lore store")
		m.LoreIn(app, "memory", "add", "--body", "a new note", "--to", "kb").OK(t)
		m.LoreIn(app, "sync").OK(t) // C5: the note reaches kb with Sam's sync
		list := m.LoreIn(app, "memory", "list").OK(t)
		list.Says(t, "a new note").Never(t, "an old note", "written after the reset")
	})

	w.Step("day1/desk-finds-the-store-started-fresh", func() {
		// B2-34: the remote holds another store now; the only way out for
		// the desk is forget + sync, and --restore says the same
		r := desk.LoreIn(deskApp, "sync")
		if r.Code == 0 {
			t.Errorf("the desk's sync must be refused: %s", r)
		}
		r.Says(t, "started fresh", "lore store forget --url "+kb.SSHURL()+" --yes", "lore sync").Never(t, "lore store init")
		desk.LoreIn(deskApp, "store", "push", kb.SSHURL(), "--restore", "--yes").Says(t, "started fresh", "lore store forget").Never(t, "store push --restore")
		desk.LoreIn(deskApp, "store", "forget", kb.SSHURL(), "--yes").OK(t)
		desk.LoreIn(deskApp, "sync").OK(t)
		desk.LoreIn(deskApp, "memory", "list").OK(t).Says(t, "a new note").Never(t, "an old note")
	})
}

// TestJourneySoloFirstInitOfAnOrgStore (B2-33 regression): the first
// `lore store init` of a brand-new store whose url the org entry already
// lists (normal admin onboarding) records it in the repo's
// .lore/stores.yaml, as always; only a store init after `lore store
// forget` of that url is "start fresh" on the org's store.
func TestJourneySoloFirstInitOfAnOrgStore(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(jsuName, jsuEmail)
	m := sam.Laptop()
	notes := w.Remote("github.com/agency/notes")
	app := m.Clone(w.Remote(jsuRepo), "site")

	w.Step("day1/first-init-of-the-org-store", func() {
		if err := os.MkdirAll(m.LorePath(), 0o700); err != nil {
			t.Fatal(err)
		}
		cfg := "orgs:\n  - name: agency\n    identity: {key: \"human:" + jsuEmail + "\", name: \"" + jsuName + "\", handle: sam}\n" +
			"    stores:\n      - {url: \"" + notes.SSHURL() + "\", org_folder: org}\n"
		if err := os.WriteFile(m.LorePath("config.yaml"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		m.LoreIn(app, "init").OK(t)
		r := m.LoreIn(app, "store", "init", "agency", notes.SSHURL(), "--yes").OK(t)
		r.Says(t, "recorded in .lore/stores.yaml").Never(t, "old store", "team step")
		b, err := os.ReadFile(filepath.Join(app.Dir, ".lore", "stores.yaml"))
		if err != nil || !strings.Contains(string(b), notes.SSHURL()) {
			t.Fatalf(".lore/stores.yaml lacks the new store (%v):\n%s", err, b)
		}
		m.LoreIn(app, "memory", "add", "--body", "a first agency note", "--to", "agency").OK(t)
	})
}
