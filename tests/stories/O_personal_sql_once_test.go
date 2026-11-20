//go:build stories && !windows

package stories

// O_personal_sql_once_test.go — story O, devco D-757 (owner P-7, bug hunt
// H-18): on a personal knowledge base `lore sql` returned every memory,
// task and incident row twice. A store's cache file is one per place on
// the machine, and every open that reads the place applies its rows under
// the name it gives the store: `--store me/<folder>` from home, the store
// clone folder, a repo whose .lore/stores.yaml attaches a folder of the
// personal store under its own name. The `<table>_all` views keep only the
// rows of the lens's own name; the plain `<table>` (and `res_<kind>`) read
// the attached table whole, so after the second name every row came back
// once per name. Here one owner-shaped laptop keeps a personal store on a
// local bare repo, reads it from all three places in turn, and every plain
// and `_all` table of every kind lists each row once, masked and with
// --show-sensitive. The same cache also dropped a personal kind's
// `res_<kind>` view (its field rows came back once per name); it is read
// here too.

import (
	"path/filepath"
	"strings"
	"testing"
)

// psFolder is the personal store folder the rows are written to.
const psFolder = "notes"

// psTables are the tables a personal knowledge base fills, plain and _all.
var psTables = []string{"memories", "tasks", "incidents", "rules", "res_server"}

// TestStoryOPersonalSQLOnce: lore sql lists each personal-store row once
// from every place, after every place has read the store (devco D-757).
func TestStoryOPersonalSQLOnce(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	code := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/side-project"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})

	to := "me/" + psFolder
	s.Step("personal-store/rows", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", psFolder).OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", "work").OK(t)
		for _, n := range []string{"first", "second"} {
			ada.Lore(ada.Home, "memory", "add", "--body", "ps "+n+" memory", "--to", to, "--yes").OK(t)
			ada.Lore(ada.Home, "task", "add", "--title", "ps "+n+" task", "--to", to, "--yes").OK(t)
			ada.Lore(ada.Home, "incident", "add", "--title", "ps "+n+" incident", "--body", "what broke", "--to", to, "--yes").OK(t)
			ada.Lore(ada.Home, "rule", "add", "--body", "ps "+n+" rule", "--to", to, "--yes").OK(t)
		}
		ada.Lore(ada.Home, "memory", "add", "--body", "ps memory in another folder", "--to", "me/work", "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "kind", "add", "--kind", "server", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "field", "add", "--kind", "server", "--field", "ip", "--type", "ip", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "add", "--kind", "server", "--name", "web_prod", "--set", "ip=203.0.113.10", "--to", to, "--yes").OK(t)
	})

	clone := filepath.Join(oStoreCloneLocal(t, ada), psFolder)
	wc := ada.CloneTo(code, ada.Code("side-project"))
	// places reads the store from each place, each giving it its own name
	// in the one cache file of the place
	places := []struct {
		name string
		dir  string
		args []string
	}{
		{"home --store " + to, ada.Home, []string{"--store", to}},
		{"store clone folder", clone, nil},
		{"repo with stores.yaml", wc.Dir, nil},
	}
	s.Step("personal-store/three-names", func() {
		ada.LoreIn(wc, "store", "add", "--name", "mine", "--url", "file://"+me.Dir, "--folder", psFolder, "--yes").OK(t)
		for _, p := range places {
			ada.Lore(p.dir, append([]string{"sql", "--query", "SELECT COUNT(*) FROM memories_all"}, p.args...)...).OK(t)
		}
	})

	s.Step("personal-store/sql-once", func() {
		for _, p := range places {
			for _, sens := range [][]string{nil, {"--show-sensitive", "--yes"}} {
				for _, tbl := range psTables {
					for _, v := range []string{tbl, tbl + "_all"} {
						if p.name == "repo with stores.yaml" && v == tbl {
							continue // the plain name is the repo's own copy there (nearest store)
						}
						args := append([]string{"sql", "--format", "csv", "--query", "SELECT COUNT(*) AS n, COUNT(DISTINCT id) AS d FROM " + v}, p.args...)
						r := ada.Lore(p.dir, append(args, sens...)...)
						n, d, ok := psCounts(r.Out())
						if r.Code != 0 || !ok || n != d || n == "0" {
							t.Errorf("[O D-757 %s %s] `lore %s` lists a personal row more than once (or none):\n%s",
								p.name, strings.Join(sens, " "), strings.Join(append(args, sens...), " "), r)
						}
					}
				}
			}
		}
	})
}

// psCounts reads the "n,d" line of a csv COUNT result.
func psCounts(out string) (string, string, bool) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	n, d, ok := strings.Cut(lines[len(lines)-1], ",")
	return n, d, ok && lines[0] == "n,d"
}

// oStoreCloneLocal is the git clone of the personal store a local `store
// init --name me` made, found under ~/.lore/stores by its folder.
func oStoreCloneLocal(t *testing.T, m *OwnerMachine) string {
	t.Helper()
	for _, pat := range []string{"*", "*/*", "*/*/*"} {
		hits, _ := filepath.Glob(filepath.Join(m.Home, ".lore", "stores", pat, psFolder))
		for _, h := range hits {
			if top := filepath.Dir(h); m.Lore(top, "store", "list").Code == 0 {
				return top
			}
		}
	}
	t.Fatalf("no personal store clone with folder %s under %s/.lore/stores", psFolder, m.Home)
	return ""
}
