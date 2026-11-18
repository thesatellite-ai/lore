//go:build stories && !windows

package stories

// O_personal_clone_test.go — story O, devco D-432 (release blocker): a
// member gets a personal store from `setup join` (personal_store local, the
// default), and with it every plain read inside the member's own store
// clone folder came back empty ("(no rules)", "(no memories)", "(no
// matches)") while sql, --store <org>/<folder> and --all-repos listed the
// rows. It needs the setup to cover the clone folder (default: true, as a
// person with one setup has it). The admin never saw it: the admin's setup
// had no personal store. Here the admin and the member read the same clone
// folder (and the clone's org/ folder), with a personal store and with it
// set to none: the folder's rows come back every time, the same as the
// admin's. A linked code repo and an unlinked own repo are read the same
// way, so the answer there is the same with and without a personal store.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// pcRead is one plain read and a row text it must list.
type pcRead struct {
	args []string
	want string
}

// pcFolderReads are the plain reads in a store clone's project folder (and
// a code repo linked to it): its rows and the org folder's rule above it.
var pcFolderReads = []pcRead{
	{[]string{"rule", "list"}, "pclone folder rule"},
	{[]string{"rule", "list"}, "pclone org rule"},
	{[]string{"memory", "list"}, "pclone folder memory"},
	{[]string{"search", "--query", "deploy"}, "pclone folder"},
	{[]string{"render", "--dry-run"}, "pclone folder rule"},
}

// TestStoryOPersonalStoreClone: plain reads in a store clone folder list the
// folder's rows with and without a personal store (devco D-432).
func TestStoryOPersonalStoreClone(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	var wc *teamharness.Clone
	s.Step("admin/org-store-rows", func() {
		wc = ada.CloneTo(web, ada.OrgDir("web"))
		oSetupFlow(t, ada, wc.Dir, oSetup, "admin", oAdminOrg, oAlias,
			map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
		ada.LoreIn(wc, "sync").OK(t)
		ada.LoreIn(wc, "store", "folder", "add", "team", oFolder).OK(t)
		ada.LoreIn(wc, "rule", "add", "--body", "pclone folder rule: deploy from main", "--to", oOrg+"/"+oFolder, "--yes").OK(t)
		ada.LoreIn(wc, "memory", "add", "--body", "pclone folder memory about deploy", "--to", oOrg+"/"+oFolder, "--yes").OK(t)
		ada.LoreIn(wc, "rule", "add", "--body", "pclone org rule for every repo", "--to", oOrg+"/org", "--yes").OK(t)
		ada.LoreIn(wc, "org", "link", "--org", oOrg, "--project", oFolder, "--yes").OK(t)
		ada.LoreIn(wc, "sync").OK(t)
	})

	// check runs every read in dir on m and fails the ones missing their row.
	check := func(cell string, m *OwnerMachine, dir string, list []pcRead) {
		t.Helper()
		for _, c := range list {
			r := m.Lore(dir, c.args...)
			if r.Code != 0 || !strings.Contains(r.Out(), c.want) {
				t.Errorf("[O D-432 %s] `lore %s` in %s does not list %q:\n%s", cell, strings.Join(c.args, " "), dir, c.want, r)
			}
		}
	}
	// clone checks the store clone's project folder and its org folder.
	clone := func(cell string, m *OwnerMachine) {
		t.Helper()
		top := oStoreClone(m, oOwner, "lore-team")
		check(cell+"/clone-folder", m, filepath.Join(top, oFolder), pcFolderReads)
		check(cell+"/clone-org", m, filepath.Join(top, "org"), []pcRead{{[]string{"rule", "list"}, "pclone org rule"}})
	}

	s.Step("admin/clone-reads", func() { clone("admin", ada) })

	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	var mwc, mpc *teamharness.Clone
	s.Step("member/join", func() {
		_, line := journeys.Invite(t, ada.Machine, wc.Dir, oMemOrg.Email)
		mwc = mo.CloneTo(web, mo.OrgDir("web"))
		mpc = mo.CloneTo(platform, mo.OrgDir("platform"))
		oSetupFlow(t, mo, mwc.Dir, oMSetup, "member", oMemOrg, oAlias, map[string]any{"invite": line}, []string{"~/code/" + oOrg + "/**"})
		mo.LoreIn(mwc, "sync").OK(t)
		// one setup on the laptop: it covers every folder, store clones too
		mo.Lore(mo.Home, "setup", "edit", "--name", oMSetup, "--default", "--yes").OK(t)
		mo.Lore(mo.Home, "memory", "add", "--body", "pclone personal note", "--to", "me", "--yes").OK(t)
	})

	// An own repo with no lore project and no link holds no folder rows to
	// shadow: with a personal store a read there lists the personal rows,
	// labelled [me] (devco P-4); without one it says lore is not set up
	// there and names the link that would read the org's folder.
	s.Step("member/reads-with-personal-store", func() {
		clone("member+me", mo)
		check("member+me/linked-repo", mo, mwc.Dir, pcFolderReads[:4])
		check("member+me/unlinked-repo", mo, mpc.Dir, []pcRead{{[]string{"memory", "list"}, "pclone personal note [me]"}})
	})
	s.Step("member/reads-without-personal-store", func() {
		mo.Lore(mo.Home, "setup", "edit", "--name", oMSetup, "--personal-store", "none", "--yes").OK(t)
		clone("member-me", mo)
		check("member-me/linked-repo", mo, mwc.Dir, pcFolderReads[:4])
		r := mo.LoreIn(mpc, "memory", "list")
		if r.Code == 0 || !strings.Contains(r.Out(), "lore org link --org") {
			t.Errorf("[O D-432 member-me/unlinked-repo] `lore memory list` in an unlinked own repo with no personal store does not refuse with the link to make:\n%s", r)
		}
	})
}
