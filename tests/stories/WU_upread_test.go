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
//	D-556: in web, with a resource of acme/sync naming web's remote, list,
//	       search, trash list and context read acme/sync and say so
//	D-557: decision show (every noun's show) names the store/folder
//	D-623: those routed reads' --json name acme/sync in a top-level
//	       "routed" field, an empty answer too; a read naming its store
//	       has none

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

	s.Step("D-556/unlinked-own-repo-lists-from-the-resource-folder", func() {
		// no resource names web yet: the refusal names org link --own
		m.Lore(wc.Dir, "trash", "list").Refused(t, "lore org link --org "+wOrg+" --project <folder> --own")
		m.Lore(m.Home, "resource", "kind", "add", "--kind", wuRepoKind, "--to", wOrg+"/org", "--setup", wSetup, "--yes").OK(t)
		m.Lore(m.Home, "resource", "field", "add", "--kind", wuRepoKind, "--field", "url", "--type", "text", "--to", wOrg+"/org", "--setup", wSetup, "--yes").OK(t)
		m.Lore(m.Home, "resource", "add", "--kind", wuRepoKind, "--name", wuRepoRes, "--set", "url="+web.SSHURL(), "--to", inSync, "--setup", wSetup, "--yes").OK(t)
		m.Lore(m.Home, "rule", "add", "--body", "deploys go through the pipeline", "--to", inSync, "--setup", wSetup, "--yes").OK(t)
		note := "read " + inSync + ": its resource " + wuRepoRes + " names this repo"
		link := "lore org link --org " + wOrg + " --project sync --own"
		m.Lore(wc.Dir, "memory", "list").OK(t).Says(t, note, link, id)
		m.Lore(wc.Dir, "rule", "list").OK(t).Says(t, note, "deploys go through the pipeline")
		m.Lore(wc.Dir, "search", "--query", "deploy").OK(t).Says(t, note, id)
		m.Lore(wc.Dir, "trash", "list").OK(t).Says(t, note)
		m.Lore(wc.Dir, "resource", "list").OK(t).Says(t, note, wuRepoRes)
		m.Lore(wc.Dir, "context", "--for-prompt", "deploy").OK(t).Says(t, note, id)
		// the repo stays untouched: nothing was created in it
		if _, err := os.Stat(filepath.Join(wc.Dir, ".lore")); err == nil {
			t.Fatalf("a read created .lore/ in %s", wc.Dir)
		}
	})

	s.Step("D-623/routed-json-names-the-folder", func() {
		// devco D-623: the note line is text only; an empty --json answer
		// (the trash is empty) said nothing of where it looked
		routed := `"routed": "` + inSync + `"`
		for _, args := range [][]string{{"trash", "list", "--json"}, {"rule", "list", "--json"}, {"resource", "list", "--json"}} {
			if out := m.Lore(wc.Dir, args...).OK(t).Out(); !strings.Contains(out, routed) {
				t.Errorf("lore %s in the unlinked repo: no %s in its --json:\n%s", strings.Join(args, " "), routed, out)
			}
		}
		// a read that names its store was not routed: no field
		if out := m.Lore(m.Home, "rule", "list", "--store", inSync, "--json").OK(t).Out(); strings.Contains(out, `"routed"`) {
			t.Errorf("rule list --store %s --json carries a routed field:\n%s", inSync, out)
		}
	})

	s.Step("D-557/every-show-names-its-folder", func() {
		dec := wuDecID.FindString(m.Lore(m.Home, "decision", "add", "--title", "ship on fridays", "--body", "never", "--to", inSync, "--setup", wSetup, "--yes").OK(t).Out())
		if dec == "" {
			t.Fatal("decision add printed no id")
		}
		m.Lore(m.Home, "decision", "show", "--id", dec).OK(t).Says(t, dec+" ["+inSync+"]")
	})
}

// wuRepoKind and wuRepoRes are the resource kind and resource naming the
// web repo by its remote (D-556).
const (
	wuRepoKind = "repo"
	wuRepoRes  = "web_app"
)

// wuDecID is a decision id in a command's output.
var wuDecID = regexp.MustCompile(`dec_[0-9a-f]+`)
