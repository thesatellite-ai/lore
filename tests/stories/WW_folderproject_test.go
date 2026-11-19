//go:build stories && !windows

package stories

// WW_folderproject_test.go — story WW, devco D-718 (owner, migrating real
// repos, brief item 12): `lore task add --to <org>/<folder>` from a code
// repo stamped the calling repo's project_id on the row instead of the
// target folder's project, so the folder's own reads (filtered by its
// project) missed it and the repo's prj_ id leaked into the folder. The
// cell: every add op (task, memory, rule, decision) x every placement an
// admin writes from (a repo linked to another folder, an org repo with a
// project of its own, the store folder of another folder, home --setup)
// writes --to acme/api, and each row file in the store clone carries the
// api folder's project (its _meta.json project_id), never the caller's.
//
// Every name, email and url is made up.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// wfpOther is the folder every write of the cell is aimed at; the linked
// repo and the store folder placement sit on wFolder.
const wfpOther = "api"

// wfpAdds are the add ops of the cell (noun -> add arguments); %s is
// replaced by a per-placement mark so each row is told apart.
var wfpAdds = map[string][]string{
	"task":     {"--title", "d718 task %s"},
	"memory":   {"--body", "d718 memory %s"},
	"rule":     {"--body", "d718 rule %s"},
	"decision": {"--title", "d718 decision %s", "--body", "one queue per folder"},
}

// wfpFind is the path of the first file named name under dir (breadth
// does not matter: a folder holds one _meta.json and ids are unique).
func wfpFind(t *testing.T, dir, name string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == name {
			found = p
		}
		return nil
	})
	return found
}

// wfpFolderProject is the project id the store folder at dir records in
// its _meta.json (the folder's own project, the one its reads filter by).
func wfpFolderProject(t *testing.T, dir string) string {
	t.Helper()
	p := wfpFind(t, dir, "_meta.json")
	if p == "" {
		t.Fatalf("the store folder %s has no _meta.json", dir)
	}
	prj, _ := wthJSON(t, p)["project_id"].(string)
	if prj == "" {
		t.Fatalf("%s names no project_id", p)
	}
	return prj
}

// wfpRowProject is the project_id of the row id in the store folder at dir
// ("" with found false when no row file of that id is there).
func wfpRowProject(t *testing.T, dir, id string) (string, bool) {
	t.Helper()
	p := wfpFind(t, dir, id+".json")
	if p == "" {
		return "", false
	}
	prj, _ := wthJSON(t, p)["project_id"].(string)
	return prj, true
}

func TestStoryWWWriteToFolderStampsItsProject(t *testing.T) {
	t.Parallel()
	s := &Story{World: journeys.NewWorld(t), ID: "WW"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	// the org repo keeps a project of its own (the owner's shape: its
	// prj_ id is the one that leaked into the folder)
	m.Lore(pc.Dir, "init", "--non-interactive", "--name=platform").OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wFolder).OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wfpOther).OK(t)
	wc := fCodeClone(s, m, web, "web")
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", wFolder, "--own", "--yes").OK(t)
	m.Lore(pc.Dir, "sync").OK(t)

	top := filepath.Join(m.Home, ".lore", "stores", "acme-co", "lore-team")
	other := wFindDir(t, top, wfpOther)
	webDir := wFindDir(t, top, wFolder)
	if other == "" || webDir == "" {
		t.Fatalf("fixture: the store clone %s has no %s or %s folder", top, wfpOther, wFolder)
	}
	to := wOrg + "/" + wfpOther

	type place struct {
		name  string
		dir   string
		extra []string
	}
	s.Step("D-718/every-add-op-every-placement-stamps-the-folders-project", func() {
		for _, p := range []place{
			{"own-linked", wc.Dir, nil},
			{"org-repo-own-project", pc.Dir, nil},
			{"store-folder", webDir, nil},
			{"home", m.Home, []string{"--setup", wSetup}},
		} {
			for noun, tmpl := range wfpAdds {
				args := []string{noun, "add", "--json", "--to", to, "--yes"}
				for _, a := range tmpl {
					args = append(args, strings.ReplaceAll(a, "%s", p.name))
				}
				r := m.Lore(p.dir, append(args, p.extra...)...).OK(t)
				id := wwJSONID(r.Stdout)
				if id == "" {
					t.Fatalf("%s: %s add --to %s printed no id:\n%s", p.name, noun, to, r)
				}
				// read the folder's project after the write: the first
				// write may be the one that records it
				want := wfpFolderProject(t, other)
				got, found := wfpRowProject(t, other, id)
				switch {
				case !found:
					t.Errorf("[WW D-718 %s/%s] the row %s is not in the store's %s folder", p.name, noun, id, wfpOther)
				case got != want:
					t.Errorf("[WW D-718 %s/%s] the row %s in %s carries project_id %s, want the folder's %s (the %s folder's is %s)", p.name, noun, id, to, got, want, wFolder, wfpFolderProject(t, webDir))
				}
			}
		}
		// the folder's own reads list what was written there
		m.Lore(wc.Dir, "task", "list", "--to", to).OK(t).Says(t, "d718 task own-linked", "d718 task home")
	})
}
