//go:build journeys && !windows

package journeys

// oldlayout_test.go — the upgrader, part three: move from the old layout
// (fixture). Ola ran an
// earlier 0.1.18 build: three org stores and her personal store, each a
// bare clone plus a linked worktree under ~/.lore/clones/<host>/…, rows
// under <folder>/.lore/data/, the org config at org/.lore/lore-org.yaml,
// one store reached through her ssh alias, one commit never pushed, a new
// row not committed, and the org link code's second clones in
// ~/.lore/clones-v017. She installs this lore and runs one command.
//
// What she checks: lore says what it moved, from where to where; every
// store lands at ~/.lore/stores/<owner>/<repo> (the alias never in a
// path, never a "~2" copy); the unpushed commit reaches the remote; each
// store gets exactly one layout commit; her notes read at the same ids;
// the next command is silent; doctor names each old clone with the exact
// command that removes it; a move cut short is finished the next time.
//
// The fixture is built by hand with git, the way the earlier build laid
// it out; the row files are real ones this lore wrote in a scratch repo.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The old layout's stores (placeholders).
const (
	olOwner    = "acme"
	olPersonal = "ola"
	olAlias    = "work-gh" // Ola's ssh alias for github.com
	olSubject  = "lore: store layout 0.1.18 — rows move from <folder>/.lore/data/ to <folder>/"
)

// olStore is one store of the fixture.
type olStore struct {
	owner, repo string
	alias       bool   // origin spelled through olAlias
	orgFolder   string // the marker's org_folder (maybe wrong)
	hasOrg      bool   // the store holds org/.lore/lore-org.yaml
	folder      string // the folder holding the rows ("." the root)
	remote      *teamharness.Remote
	ids         []string // the memories seeded
}

// origin is the old clone's origin url, as Ola spelled it.
func (s *olStore) origin() string {
	host := "github.com"
	if s.alias {
		host = olAlias
	}
	return "git@" + host + ":" + s.owner + "/" + s.repo + ".git"
}

// oldBase is the old clone's stem under ~/.lore/clones.
func (s *olStore) oldBase(m *Machine) string {
	host := "github.com"
	if s.alias {
		host = olAlias
	}
	return m.LorePath("clones", host, s.owner, s.repo)
}

// newDir is where the store lands.
func (s *olStore) newDir(m *Machine) string { return m.LorePath("stores", s.owner, s.repo) }

// rowPath is a memory's old-layout path in the store.
func (s *olStore) oldRow(id string) string {
	return filepath.Join(s.folder, ".lore", "data", "memories", id+".json")
}

// flatRow is a memory's plain-layout path in the store (the root
// folder's, the store's own rows, in _store/).
func (s *olStore) flatRow(id string) string {
	folder := s.folder
	if folder == "." {
		folder = "_store"
	}
	return filepath.ToSlash(filepath.Join(folder, "memories", id+".json"))
}

var olMemID = regexp.MustCompile(`mem_[0-9a-f]{32}`)

// olMetaKey keys the folder _meta.json in the rows map.
const olMetaKey = "_meta.json"

func TestJourneyUpgraderOldLayout(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ola := w.Persona("Ola Old", "ola.old@example.test")
	m := ola.Laptop()
	// Tom shares lore-hub; his laptop also has the earlier build's clone,
	// and stays shut until Ola has moved hers
	tom := w.Persona("Tom Mate", "tom.mate@example.test")
	tm := tom.Laptop()
	tm.Sleep()
	stores := []*olStore{
		{owner: olOwner, repo: "lore-hub", orgFolder: "org", hasOrg: true, folder: "team"},
		{owner: olOwner, repo: "lore-west", orgFolder: "org", folder: "client_west"}, // a wrong org_folder
		{owner: olOwner, repo: "lore-east", alias: true, folder: "client_east"},
		{owner: olPersonal, repo: "lore-me", folder: "."},
	}
	scratch := m.Clone(w.Remote("github.com/ola/scratch"), "scratch")
	rows := map[string]string{} // id -> row file bytes; olMetaKey -> the folder's _meta.json
	bodies := map[string]string{}
	var pendingID, untrackedID string

	w.Step("day1/install-over-the-old-build", func() {
		// real row files: this lore writes them in a code repo, where rows
		// still sit under .lore/data, byte for byte as the old stores had them
		scratch.Lore("init", "--non-interactive", "--name=scratch").OK(t)
		for i := range len(stores)*2 + 2 {
			body := "old layout note " + string(rune('a'+i))
			id := olMemID.FindString(scratch.Lore("memory", "add", "--body", body).OK(t).Out())
			if id == "" {
				t.Fatalf("memory add printed no id")
			}
			b, ok := scratch.ReadFile(filepath.Join(".lore", "data", "memories", id+".json"))
			if !ok {
				t.Fatalf("no row file for %s", id)
			}
			rows[id], bodies[id] = b, body
		}
		var all []string
		for id := range rows {
			all = append(all, id)
		}
		meta, ok := scratch.ReadFile(filepath.Join(".lore", "data", "_meta.json"))
		if !ok {
			t.Fatal("the scratch repo has no _meta.json")
		}
		rows[olMetaKey] = meta
		for i, s := range stores {
			s.ids = all[i*2 : i*2+2]
			s.remote = w.Remote("github.com/" + s.owner + "/" + s.repo)
			olSeed(t, w, s, rows)
		}
		pendingID, untrackedID = all[len(stores)*2], all[len(stores)*2+1]
		// Ola's machine as the earlier build left it
		if err := os.MkdirAll(m.LorePath("clones"), 0o700); err != nil {
			t.Fatal(err)
		}
		olAliasRoute(t, w, m)
		for _, s := range stores {
			olOldClone(t, w, m, s)
		}
		if err := os.MkdirAll(tm.LorePath("clones"), 0o700); err != nil {
			t.Fatal(err)
		}
		olOldClone(t, w, tm, stores[0])
		hub, west := stores[0], stores[1]
		work := filepath.Join(hub.oldBase(m), "work")
		olWrite(t, filepath.Join(work, hub.oldRow(pendingID)), rows[pendingID])
		w.Git(work, "add", "-A", hub.folder)
		w.Git(work, "-c", "user.name=Ola Old", "-c", "user.email=ola.old@example.test", "commit", "-q", "-m", "lore: memory.add (never pushed)")
		olWrite(t, filepath.Join(west.oldBase(m), "work", west.oldRow(untrackedID)), rows[untrackedID])
		v017 := m.LorePath("clones-v017", "github.com", olOwner, "lore-hub.git")
		if err := os.MkdirAll(filepath.Dir(v017), 0o700); err != nil {
			t.Fatal(err)
		}
		w.Git(m.Home, "init", "-q", "--bare", v017)

		r := m.Lore(m.Home, "doctor")
		for _, s := range stores {
			r.Says(t, "moved store clone "+s.oldBase(m)+" -> "+s.newDir(m))
		}
		r.Says(t, "1 commit(s) the remote does not have carried", "1 uncommitted file(s) carried",
			"pushed store lore-hub", "rows moved from <folder>/.lore/data/ to <folder>/, the store's own rows to _store/", "clones-v017")
		for _, s := range stores {
			if _, err := os.Stat(filepath.Join(s.newDir(m), ".git")); err != nil {
				t.Errorf("%s/%s did not land at %s", s.owner, s.repo, s.newDir(m))
			}
			if _, err := os.Stat(s.oldBase(m) + ".git"); err != nil {
				t.Errorf("the old clone of %s was removed", s.repo)
			}
		}
		for _, wrong := range []string{m.LorePath("stores", olAlias), m.LorePath("stores", olOwner, "lore-east~2")} {
			if _, err := os.Stat(wrong); err == nil {
				t.Errorf("%s exists: the alias clone must land at its url's path", wrong)
			}
		}
	})

	w.Fact("each store has its rows right under the folder on the remote, one layout commit, lore_store 4", func(c Ctx) {
		for _, s := range stores {
			files := strings.Join(s.remote.Files("main"), "\n")
			for _, id := range s.ids {
				if !strings.Contains(files, s.flatRow(id)) {
					c.Errorf("%s: %s is not on the remote", s.repo, s.flatRow(id))
				}
			}
			if strings.Contains(files, ".lore/") {
				c.Errorf("%s: old paths still on the remote:\n%s", s.repo, files)
			}
			if n := strings.Count(strings.Join(s.remote.Log("main"), "\n"), olSubject); n != 1 {
				c.Errorf("%s: %d layout commits on the remote, want 1", s.repo, n)
			}
			marker, _ := s.remote.Read("main", "lore-store.yaml")
			if !strings.Contains(marker, "lore_store: 4") {
				c.Errorf("%s: marker %q, want lore_store: 4", s.repo, marker)
			}
			if wantOrg := s.hasOrg; strings.Contains(marker, "org_folder") != wantOrg {
				c.Errorf("%s: marker %q; org_folder only where the org folder is", s.repo, marker)
			}
			if _, ok := s.remote.Read("main", "README.md"); !ok {
				c.Errorf("%s: no README.md", s.repo)
			}
		}
		hub := stores[0]
		if _, ok := hub.remote.Read("main", "org/lore-org.yaml"); !ok {
			c.Errorf("lore-hub: org/lore-org.yaml is not on the remote")
		}
		if _, ok := hub.remote.Read("main", hub.flatRow(pendingID)); !ok {
			c.Errorf("lore-hub: the commit that was never pushed did not reach the remote")
		}
	})

	w.Step("day1/notes-read-at-the-same-ids", func() {
		for _, s := range stores {
			dir := filepath.Join(s.newDir(m), s.folder)
			for _, id := range s.ids {
				m.Lore(dir, "show", id).OK(t).Says(t, bodies[id])
			}
		}
		west := stores[1]
		if _, err := os.Stat(filepath.Join(west.newDir(m), filepath.FromSlash(west.flatRow(untrackedID)))); err != nil {
			t.Errorf("the uncommitted row of lore-west did not move with its folder: %v", err)
		}
	})

	w.Step("day2/the-next-command-is-quiet", func() {
		w.AdvanceDays(1)
		r := m.Lore(m.Home, "doctor")
		r.Never(t, "moved store clone", "rows moved", "pushed store")
		for _, s := range stores {
			r.Says(t, "old_clones", "rm -rf "+s.oldBase(m)+" "+s.oldBase(m)+".git")
		}
		r.Says(t, "rm -rf "+m.LorePath("clones-v017", "github.com", olOwner, "lore-hub.git"))
	})

	w.Step("day2/a-teammate-upgrades-after-the-layout-commit", func() {
		tm.Wake()
		hub := stores[0]
		r := tm.Lore(tm.Home, "doctor")
		r.Says(t, "moved store clone "+hub.oldBase(tm)+" -> "+hub.newDir(tm))
		r.Never(t, "rows moved") // Ola's layout commit is fetched, not made again
		for _, id := range hub.ids {
			tm.Lore(filepath.Join(hub.newDir(tm), hub.folder), "show", id).OK(t).Says(t, bodies[id])
		}
	})

	w.Step("day3/a-move-cut-short-is-finished", func() {
		w.AdvanceDays(1)
		late := &olStore{owner: olOwner, repo: "lore-late", folder: "team", ids: []string{pendingID}}
		late.remote = w.Remote("github.com/" + late.owner + "/" + late.repo)
		olSeed(t, w, late, rows)
		olOldClone(t, w, m, late)
		half := late.newDir(m) + ".moving"
		olWrite(t, filepath.Join(half, "README"), "cut short\n")
		m.Lore(m.Home, "doctor").Says(t, "moved store clone "+late.oldBase(m)+" -> "+late.newDir(m), "rows moved")
		if _, err := os.Stat(half); err == nil {
			t.Errorf("the half-built %s is left", half)
		}
		m.Lore(filepath.Join(late.newDir(m), "team"), "show", pendingID).OK(t).Says(t, bodies[pendingID])
	})
}

// olWrite writes a file, making its folders.
func olWrite(t testing.TB, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// olSeed pushes the store's old-layout tree to its remote: the marker of
// the earlier build, the rows under <folder>/.lore/data, the org config
// under org/.lore.
func olSeed(t testing.TB, w *World, s *olStore, rows map[string]string) {
	t.Helper()
	seed := filepath.Join(w.Root, "seed-"+s.repo)
	w.Git(w.Root, "init", "-q", "-b", "main", seed)
	id, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	marker := "lore_store: 2\nid: " + id.String() + "\nvisibility: internal\ncreated: \"2026-01-01\"\n"
	if s.orgFolder != "" {
		marker += "org_folder: " + s.orgFolder + "\n"
	}
	olWrite(t, filepath.Join(seed, "lore-store.yaml"), marker)
	if s.hasOrg {
		olWrite(t, filepath.Join(seed, "org", ".lore", "lore-org.yaml"), "name: "+olOwner+"\nstores:\n  - url: git@github.com:"+olOwner+"/lore-hub.git\n    org_folder: org\n")
	}
	for _, id := range s.ids {
		olWrite(t, filepath.Join(seed, s.oldRow(id)), rows[id])
	}
	olWrite(t, filepath.Join(seed, s.folder, ".lore", "data", olMetaKey), rows[olMetaKey])
	w.Git(seed, "add", "-A")
	w.Git(seed, "-c", "user.name=Earlier Build", "-c", "user.email=build@example.test", "commit", "-q", "-m", "lore: old layout")
	w.Git(seed, "push", "-q", s.remote.Dir, "main")
}

// olOldClone lays out the store's clone the way the earlier build did: a
// bare clone at clones/<host>/<owner>/<repo>.git with Ola's origin, its
// tracking branches fetched, a linked worktree at <repo>/work on main,
// the clone lock, and the worktree's own .lore/lock.
func olOldClone(t testing.TB, w *World, m *Machine, s *olStore) {
	t.Helper()
	base := s.oldBase(m)
	bare := base + ".git"
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	w.Git(m.Home, "init", "-q", "--bare", bare)
	w.Git(bare, "remote", "add", "origin", s.origin())
	w.Git(bare, "fetch", "-q", s.remote.Dir, "+refs/heads/*:refs/remotes/origin/*")
	w.Git(bare, "worktree", "add", "-q", "-b", "main", filepath.Join(base, "work"), "refs/remotes/origin/main")
	olWrite(t, filepath.Join(base, "lock"), "")
	olWrite(t, filepath.Join(base, "work", ".lore", "lock"), "")
}

// olAliasRoute gives Ola's machine her ssh alias: git@work-gh: reaches the
// fake github.com, as her ~/.ssh/config would (an insteadOf in her own git
// config stands in for it).
func olAliasRoute(t testing.TB, w *World, m *Machine) {
	t.Helper()
	gc := filepath.Join(m.Home, ".gitconfig")
	target := filepath.Join(w.Root, "hosts", "github.com") + "/"
	if r := w.TryGit(m.Home, "config", "--file", gc, "--add", "url."+target+".insteadOf", "git@"+olAlias+":"); r.Code != 0 {
		t.Fatalf("alias route: %s", r.Stderr)
	}
}
