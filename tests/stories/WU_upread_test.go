//go:build stories && !windows

package stories

// WU_upread_test.go — story WU, devco D-550..D-555 (owner use pass 1,
// reads from anywhere): an org admin stands in the home folder (under no
// setup's folders) and in an own repo of the org that is not linked to a
// store folder, and reads the org's rows by id, by store and by kind.
//
//	day1: StartTeam in platform, own: github.com/acme-co/*, a memory
//	      written to acme/sync from the home folder, web cloned unlinked
//	D-550: resource list --store acme answers without --kind (home, web)
//	D-551: in web, memory show --id reads the row memory edit --id edits;
//	       a read that still needs a project names org link --own for acme
//	D-552: --store acme/<other folder> says where the row is
//	D-553: memory show --id from the home folder finds it; the text names
//	       the store/folder
//	D-554: store list in the home folder names store list --setup
//	D-555: context --store acme[/sync] from the home folder and from web

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// wuMemID is a memory id in a command's output.
var wuMemID = regexp.MustCompile(`mem_[0-9a-f]+`)

func TestStoryWUReadsFromAnywhere(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WU"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	add := m.Lore(m.Home, "memory", "add", "--body", "the deploy key rotates monthly", "--to", wOrg+"/sync", "--setup", wSetup, "--yes").OK(t)
	id := wuMemID.FindString(add.Out())
	if id == "" {
		t.Fatalf("memory add printed no id: %s", add.Out())
	}
	inSync := wOrg + "/sync"

	s.Step("D-550/resource-list-store-without-kind", func() {
		m.Lore(m.Home, "resource", "list", "--store", wOrg).OK(t).Says(t, "(no resources)")
		m.Lore(wc.Dir, "resource", "list", "--store", wOrg).OK(t).Says(t, "(no resources)")
		m.Lore(m.Home, "resource", "list", "--store", wOrg, "--where", "a=b").Refused(t, "need --kind", "lore resource kinds")
	})

	s.Step("D-551/unlinked-own-repo-reads-by-id", func() {
		m.Lore(wc.Dir, "memory", "show", "--id", id).OK(t).Says(t, id+" ["+inSync+"]", "the deploy key rotates monthly")
		m.Lore(wc.Dir, "memory", "edit", "--id", id, "--body", "the deploy key rotates weekly").OK(t)
		m.Lore(wc.Dir, "memory", "show", "--id", id).OK(t).Says(t, "the deploy key rotates weekly").Never(t, "landed on main")
		m.Lore(wc.Dir, "trash", "list").Refused(t, "lore org link --org "+wOrg+" --project <folder> --own", "--store "+wOrg)
	})

	s.Step("D-552/named-folder-without-the-row", func() {
		m.Lore(m.Home, "memory", "show", "--id", id, "--store", wOrg+"/org").
			Refused(t, "it is in "+inSync, "lore memory show --id "+id+" --store "+inSync).Never(t, "sync peek", "landed on main")
	})

	s.Step("D-553/home-reads-by-id", func() {
		m.Lore(m.Home, "memory", "show", "--id", id).OK(t).Says(t, id+" ["+inSync+"]", "the deploy key rotates weekly")
	})

	s.Step("D-554/store-list-names-the-setup", func() {
		m.Lore(m.Home, "store", "list").OK(t).Says(t, "lore store list --setup "+wSetup)
		m.Lore(m.Home, "store", "list", "--setup", wSetup).OK(t).Says(t, wOrg+"/org").Never(t, "hint: no setup covers")
	})

	s.Step("D-555/context-store", func() {
		m.Lore(m.Home, "context", "--store", wOrg, "--for-prompt", "deploy").OK(t).Says(t, id, "rotates weekly")
		m.Lore(m.Home, "context", "--store", inSync, "--for-prompt", "deploy").OK(t).Says(t, id)
		m.Lore(wc.Dir, "context", "--store", wOrg, "--for-prompt", "deploy").OK(t).Says(t, id)
	})
}
