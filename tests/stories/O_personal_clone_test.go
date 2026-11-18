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
	"os/exec"
	"path/filepath"
	"regexp"
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
	// devco D-640: `--to me` inside the store clone folder said "no store
	// named me is attached; your personal store is not set up" although
	// whoami there showed the personal store; the hint made a second one.
	// The write and a read of me work there as from home, and the folder's
	// plain reads still list the folder's rows (D-432).
	s.Step("member/to-me-in-clone-folder", func() {
		dir := filepath.Join(oStoreClone(mo, oOwner, "lore-team"), oFolder)
		r := mo.Lore(dir, "memory", "add", "--to", "me", "--body", "pclone note written in the clone folder", "--yes")
		if r.Code != 0 || strings.Contains(r.Out(), "not set up") {
			t.Fatalf("[O D-640] `lore memory add --to me` in the store clone folder is refused:\n%s", r)
		}
		check("member+me/clone-folder-me", mo, dir, []pcRead{
			{[]string{"memory", "list", "--store", "me"}, "pclone note written in the clone folder"},
			{[]string{"memory", "list", "--store", "me"}, "pclone personal note"},
		})
		check("member+me/clone-folder-after-me", mo, dir, pcFolderReads)
		if r := mo.Lore(dir, "memory", "list"); strings.Contains(r.Out(), "pclone note written in the clone folder") {
			t.Errorf("[O D-640] a plain read in the store clone folder lists the personal note:\n%s", r)
		}
	})
	// devco:D-642: a member's first write registered their agent actor in
	// the org store while the row went elsewhere, and that commit read
	// "lore: memory add act_…" (a memory that is not in it). No commit of
	// the member's store clone names an actor id under another command's
	// words; an actor-only commit says "actor register" or "actor copy".
	s.Step("member/actor-commit-subjects", func() {
		// the member's agent writes (detection on, a claude_code session):
		// its actor row is registered by this write
		agent := mwc.With("LORE_HUMAN=", "LORE_NO_DETECT=0", "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=o-d642")
		mo.LoreIn(agent, "memory", "add", "--body", "pclone agent note in the linked repo", "--yes").OK(t)
		mo.LoreIn(mwc, "sync").OK(t)
		top := oStoreClone(mo, oOwner, "lore-team")
		out, err := exec.Command("git", "-C", top, "log", "--all", "--format=%s").Output()
		if err != nil {
			t.Fatalf("git log in %s: %v", top, err)
		}
		for _, subj := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if m := oActorUnderOtherOp.FindStringSubmatch(subj); m != nil && !strings.HasPrefix(m[1], "actor ") {
				t.Errorf("[O D-642] store commit %q names an actor row under %q", subj, m[1])
			}
		}
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

// oActorUnderOtherOp matches a store commit subject naming an actor id
// right after its operation words ("lore: memory add act_…"): group 1 is
// the words (devco:D-642).
var oActorUnderOtherOp = regexp.MustCompile(`^lore: ([a-z ]+?) act_[0-9a-f]+`)
