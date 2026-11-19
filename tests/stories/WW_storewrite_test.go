//go:build stories && !windows

package stories

// WW_storewrite_test.go — story WW, devco D-693: an admin's company writes
// from every placement, the store clone's top and folder included. A store
// clone sits under no setup's folders and its remote is the org's store,
// not a code repo: before the fix no setup and no org resolved there, so
// the admin wrote as the OS user and was refused "not an admin of acme",
// and `--to acme/web` naming the folder itself was refused E_NOT_A_STORE.
//
//	D-693/admin-every-placement: (store-root, store-folder, own-linked,
//	       client, home --setup) x (rule add --to <org>/<folder>, policy
//	       set --to <org>/<folder>, rule edit --severity): the admin's
//	       write lands; in the store folder the plain folder and the
//	       folder with no --to land too; whoami there names the org's
//	       setup and identity
//	D-693/member-refused-every-placement-admin-runs-printed: the same
//	       five placements x (member, admin): the member's policy set and
//	       rule edit on <org>/<folder> are refused with the ask-an-admin
//	       hint, the admin runs the printed line from the same placement

import (
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

var wwsRuleID = regexp.MustCompile(`rul_[0-9a-z]+`)

func TestStoryWWStoreFolderWrites(t *testing.T) {
	t.Parallel()
	w := wwBuild(t)
	m := w.m
	s := w.s
	s.Step("D-693/whoami-in-store-folder", func() {
		for _, p := range []string{"store-folder", "store-root"} {
			m.Lore(w.places[p].Dir, "whoami").OK(t).Says(t, wSetup, wwAcme.Email)
		}
		m.Lore(w.places["ops-folder"].Dir, "whoami").OK(t).Says(t, wwOpsSetup, wwOpsID.Email)
	})
	s.Step("D-693/admin-every-placement", func() {
		type place struct {
			name  string
			extra []string
		}
		for i, p := range []place{
			{"store-folder", nil}, {"store-root", nil}, {"own-linked", nil}, {"client", nil},
			{"home", []string{"--setup", wSetup}},
		} {
			dir := w.places[p.name].Dir
			mark := "d693 rule " + strconv.Itoa(i)
			for _, to := range []string{wOrg + "/" + wFolder, wOrg + "/org"} {
				out := m.Lore(dir, append([]string{"rule", "add", "--to", to, "--severity", "must", "--body", mark + " " + to, "--yes"}, p.extra...)...).OK(t).Out()
				id := wwsRuleID.FindString(out)
				if id == "" {
					t.Fatalf("%s: rule add --to %s printed no id:\n%s", p.name, to, out)
				}
				m.Lore(dir, append([]string{"rule", "edit", "--id", id, "--severity", "should"}, p.extra...)...).OK(t)
				// the folder's budget stays at least as strict as the org's
				v := 30000 - i
				if to == wOrg+"/org" {
					v = 90000 - i
				}
				m.Lore(dir, append([]string{"policy", "set", "--key", wubKey, "--value", strconv.Itoa(v), "--to", to, "--yes"}, p.extra...)...).OK(t)
			}
		}
		f := w.places["store-folder"].Dir
		m.Lore(f, "rule", "add", "--to", wFolder, "--body", "d693 plain folder", "--yes").OK(t)
		m.Lore(f, "rule", "add", "--body", "d693 no to", "--yes").OK(t)
		m.Lore(f, "policy", "set", "--key", wubKey, "--value", "25000", "--yes").OK(t)
		of := w.places["ops-folder"].Dir
		m.Lore(of, "rule", "add", "--to", wwOpsOrg+"/"+wFolder, "--severity", "must", "--body", "d693 ops", "--yes").OK(t)
		m.Lore(of, "policy", "set", "--key", wubKey, "--value", "32000", "--to", wwOpsOrg+"/"+wFolder, "--yes").OK(t)
	})
}

// TestStoryWWStoreFolderMember is D-693's member half, one table with
// the admin story's five placements (store folder, store top, own-linked
// repo, client repo, home --setup) x (member, admin): the member resolves
// as themself everywhere, so the company-ground writes on <org>/<folder>
// are refused with the ask-an-admin hint naming the org's admin, and the
// admin runs the printed command as printed from the same placement.
func TestStoryWWStoreFolderMember(t *testing.T) {
	t.Parallel()
	s := &Story{World: journeys.NewWorld(t), ID: "WW"}
	store := s.Remote(wStoreSlug)
	cstore := s.Remote(wCStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote(wShopSlug), func(dir string) { Monorepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(pc.Dir, "member", "add", "--email", wubMember.Email, "--name", wubMember.Name, "--handle", "mo", "--role", "member", "--yes").OK(t)
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wubFolder).OK(t)
	wc := fCodeClone(s, m, web, "web")
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", wubFolder, "--own", "--yes").OK(t)
	sc := fCodeClone(s, m, shop, "shop")
	m.Lore(pc.Dir, "org", "store", "add", "--org", wOrg, "--url", cstore.SSHURL(), "--yes").OK(t)
	m.Lore(sc.Dir, "org", "link", "--org", wOrg, "--project", wClientProj, "--store", cstore.SSHURL(), "--yes").OK(t)
	to := wOrg + "/" + wubFolder
	must := wwsRuleID.FindString(m.Lore(pc.Dir, "rule", "add", "--to", to, "--severity", "must", "--body", "d693 member cell rule", "--yes").OK(t).Out())
	m.Lore(pc.Dir, "sync").OK(t)
	m.Lore(wc.Dir, "sync").OK(t)
	m.Lore(sc.Dir, "sync").OK(t)

	mo := s.Persona(wubMember.Name, wubMember.Email).Laptop()
	journeys.GitHubLogin(t, s.World, mo, wubMember.Login)
	mc := fCodeClone(s, mo, web, "web")
	journeys.Join(t, mo, mc.Dir, wubJoin, store.SSHURL(), wubMember, []string{wFolders})
	ms := fCodeClone(s, mo, shop, "shop")
	mo.Lore(mc.Dir, "sync").OK(t)
	mo.Lore(ms.Dir, "sync").OK(t)

	type place struct {
		name                string
		member, admin       string
		memberXtra, adminXt []string
	}
	mtop := filepath.Join(mo.Home, ".lore", "stores", "acme-co", "lore-team")
	atop := filepath.Join(m.Home, ".lore", "stores", "acme-co", "lore-team")
	places := []place{
		{name: "store-folder", member: filepath.Join(mtop, wubFolder), admin: filepath.Join(atop, wubFolder)},
		{name: "store-root", member: mtop, admin: atop},
		{name: "own-linked", member: mc.Dir, admin: wc.Dir},
		{name: "client", member: ms.Dir, admin: sc.Dir},
		{name: "home", member: mo.Home, admin: m.Home, memberXtra: []string{"--setup", wubJoin}, adminXt: []string{"--setup", wSetup}},
	}
	s.Step("D-693/member-refused-every-placement-admin-runs-printed", func() {
		ask := "ask an admin of " + wOrg + " (" + wAdmin.Name + ")"
		for i, p := range places {
			mo.Lore(p.member, append([]string{"whoami"}, p.memberXtra...)...).OK(t).Says(t, wubMember.Email)
			for _, args := range [][]string{
				{"policy", "set", "--key", wubKey, "--value", strconv.Itoa(40000 - i), "--to", to, "--yes"},
				{"rule", "edit", "--id", must, "--severity", "should"},
			} {
				r := mo.Lore(p.member, append(args, p.memberXtra...)...).Refused(t, "E_REFUSED", "only an admin of the org", ask)
				line := wogPrintedLine(t, r.Out())
				m.Lore(p.admin, append(wubFields(t, line), p.adminXt...)...).OK(t)
				m.Lore(p.admin, append([]string{"rule", "edit", "--id", must, "--severity", "must"}, p.adminXt...)...).OK(t)
			}
		}
		mo.Lore(filepath.Join(mtop, wubFolder), "rule", "show", "--id", must).OK(t).Says(t, "must")
	})
}
