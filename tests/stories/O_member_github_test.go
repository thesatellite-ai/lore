//go:build stories && !windows

package stories

// O_member_github_test.go — `lore member edit --github` on the
// owner-shaped machine (story O, devco D-961, the H-218 / #161 follow-up).
// D-959 gave member edit a --github flag (the login is the person's primary
// github contact; the old login stays theirs as an identity contact; admin
// only; another person's login is E_CONTACT_TAKEN). These are the
// ownershape cells for it, where an admin really runs it:
//
//	home folder via --setup   the machine has two org setups, so every
//	                          command printed there (the add refusal's
//	                          edit, the edit's --handle line) runs as
//	                          printed from the same folder
//	inside the store clone    the clone of the org store is the roster
//	                          (no --setup needed)
//	a member (not an admin)   refused with the role message, nothing written
//	another person's login    E_CONTACT_TAKEN, nothing written
//	after the edit            the join prefill names the new login, the old
//	                          login still names the person (actor show,
//	                          task --assign), and rows the person wrote
//	                          before still show them

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// oHintArgs is the argv of the "hint: lore …" line of out (nil when none).
func oHintArgs(out string) []string {
	for _, l := range strings.Split(out, "\n") {
		if h, ok := strings.CutPrefix(strings.TrimSpace(l), "hint: lore "); ok {
			return strings.Fields(h)
		}
	}
	return nil
}

// oHandleLine is the `lore member edit … --handle … --yes` the edit's note
// prints (nil when none).
func oHandleLine(out string) []string {
	i := strings.Index(out, "(lore member edit ")
	if i < 0 {
		return nil
	}
	rest := out[i+len("(lore "):]
	j := strings.Index(rest, " --yes")
	if j < 0 {
		return nil
	}
	return strings.Fields(rest[:j+len(" --yes")])
}

func TestStoryOMemberEditGitHub(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	kiteStore := s.Remote("github.com/kite-me/lore-kite")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })
	kiteApp := s.Seed(s.Remote("github.com/kite-me/app"), func(dir string) { PlainRepo(t, dir) })

	const (
		oldLogin = "pia-home"
		newLogin = "pia-acme"
	)
	pia := journeys.Person{Name: "Pia Hire", Email: "pia@acme.example", Login: newLogin}

	// the admin's machine has two org setups, so a command from the home
	// folder needs --setup (D-692) and a printed line must carry it
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	kc := ada.CloneTo(kiteApp, ada.Code("kite", "app"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "sync").OK(t)
	oSetupFlow(t, ada, kc.Dir, oKSetup, "admin", oAdminPersonal, "github.com",
		map[string]any{"org": oKite, "store_url": kiteStore.SSHURL()}, []string{"~/code/kite/**"})
	ada.LoreIn(kc, "sync").OK(t)
	ada.Lore(ada.Home, "member", "add", "--email", pia.Email, "--name", pia.Name, "--github", oldLogin, "--setup", oSetup, "--yes").OK(t).
		Says(t, "handle @"+oldLogin)
	ada.Lore(ada.Home, "member", "add", "--email", oMemOrg.Email, "--name", oMemOrg.Name, "--github", oMemOrg.Login, "--role", "member", "--setup", oSetup, "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)
	teamClone := oStoreClone(ada, oOwner, "lore-team")
	ada.LoreIn(pc, "store", "folder", "add", "team", oFolder).OK(t)
	ada.LoreIn(pc, "sync").OK(t)
	ada.Lore(teamClone, "sync").OK(t)
	folder := filepath.Join(teamClone, oFolder)

	// the member joins on their own machine (one org setup there)
	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	mwc := mo.CloneTo(web, mo.OrgDir("web"))
	oSetupFlow(t, mo, mwc.Dir, oMSetup, "member", oMemOrg, oAlias,
		map[string]any{"invite": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	// a row the hire is assigned under the old login, before the change
	// assignOld adds a task assigned by the old login; its id
	assignOld := func(t *testing.T, title string) string {
		t.Helper()
		r := ada.Lore(folder, "task", "add", "--title", title, "--assign", "github:"+oldLogin, "--json").OK(t)
		var got struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil || got.Data.ID == "" {
			t.Fatalf("task add --json: %v\n%s", err, r)
		}
		return got.Data.ID
	}
	var taskID string
	s.Step("D-961/before-a-task-assigned-by-the-old-login", func() {
		taskID = assignOld(t, "pia's onboarding checklist")
		ada.Lore(folder, "task", "show", "--id", taskID).OK(t).Says(t, pia.Name)
		ada.Lore(teamClone, "sync").OK(t)
	})

	// cell home × --setup: the add's refusal and the edit's --handle note
	// each print a command that runs as printed from the home folder
	s.Step("D-961/cell-home-via-setup", func() {
		r := ada.Lore(ada.Home, "member", "add", "--email", pia.Email, "--github", newLogin, "--setup", oSetup, "--dry-run").Fails(t, 20, "E_INVALID_INPUT")
		s.NamedFlags(t, r)
		args := oHintArgs(r.Out())
		if args == nil {
			t.Fatalf("the add refusal prints no hint:\n%s", r)
		}
		r = ada.Lore(ada.Home, args...)
		oCheck(t, "D-961/home-add-hint-runs", r.Code == 0, "the hint the add refusal printed from the home folder (lore %s) does not run there:\n%s", strings.Join(args, " "), r)
		if r.Code != 0 {
			// the cell's own edit, so the steps after it still check
			r = ada.Lore(ada.Home, "member", "edit", "--who", pia.Email, "--github", newLogin, "--setup", oSetup, "--yes").OK(t)
		}
		r.Says(t, "earlier GitHub login "+oldLogin+" stays theirs", "the handle @"+oldLogin+" is unchanged")
		s.NamedFlags(t, r)
		hl := oHandleLine(r.Out())
		if hl == nil {
			t.Fatalf("the edit prints no --handle line:\n%s", r)
		}
		h := ada.Lore(ada.Home, hl...)
		oCheck(t, "D-961/home-handle-line-runs", h.Code == 0, "the --handle line the edit printed from the home folder (lore %s) does not run there:\n%s", strings.Join(hl, " "), h)
		ada.Lore(ada.Home, "member", "show", "--who", pia.Email, "--setup", oSetup).OK(t).Says(t, "github:   "+newLogin)
		ada.LoreIn(pc, "sync").OK(t)
	})

	// cell inside the store clone: the clone is the roster; an edit there
	// and back needs no --setup
	s.Step("D-961/cell-inside-store-clone", func() {
		const tmp = "pia-acme-two"
		ada.Lore(teamClone, "member", "edit", "--who", pia.Email, "--github", tmp, "--yes").OK(t).
			Says(t, "earlier GitHub login "+newLogin+" stays theirs")
		ada.Lore(teamClone, "member", "show", "--who", pia.Email).OK(t).Says(t, "github:   "+tmp)
		ada.Lore(teamClone, "member", "edit", "--who", pia.Email, "--github", newLogin, "--yes").OK(t)
		ada.Lore(teamClone, "member", "show", "--who", pia.Email).OK(t).Says(t, "github:   "+newLogin)
		ada.Lore(teamClone, "sync").OK(t)
	})

	// cell a member: refused with the role message, nothing pushed
	s.Step("D-961/cell-member-is-refused", func() {
		mo.LoreIn(mwc, "sync").OK(t)
		before := s.RefsOf(team)
		r := mo.LoreIn(mwc, "member", "edit", "--who", pia.Email, "--github", "pia-sneaky", "--yes")
		if r.Code == 0 {
			t.Fatalf("a member changed another person's GitHub login:\n%s", r)
		}
		r.Says(t, "admin")
		s.NamedFlags(t, r)
		mo.Lore(mo.Home, "member", "edit", "--who", pia.Email, "--github", "pia-sneaky", "--setup", oMSetup, "--yes").Refused(t, "admin")
		mo.LoreIn(mwc, "sync").OK(t)
		s.NoPushSince(t, team, before, "a member's refused member edit --github")
		mo.LoreIn(mwc, "member", "show", "--who", pia.Email).OK(t).Says(t, "github:   "+newLogin).Never(t, "pia-sneaky")
	})

	// cell another person's login: E_CONTACT_TAKEN, the roster unchanged
	s.Step("D-961/cell-taken-login", func() {
		for _, dir := range []string{ada.Home, teamClone} {
			args := []string{"member", "edit", "--who", pia.Email, "--github", oMemOrg.Login, "--yes"}
			if dir == ada.Home {
				args = append(args, "--setup", oSetup)
			}
			r := ada.Lore(dir, args...).Refused(t, "E_CONTACT_TAKEN")
			s.NamedFlags(t, r)
		}
		ada.Lore(teamClone, "member", "show", "--who", pia.Email).OK(t).Says(t, "github:   "+newLogin).Never(t, "github:   "+oMemOrg.Login)
		ada.Lore(teamClone, "member", "show", "--who", oMemOrg.Email).OK(t).Says(t, "github:   "+oMemOrg.Login)
	})

	// after the edit: the hire's own join prefills the new login; the old
	// login still names them; the task assigned under it is still theirs
	s.Step("D-961/after-the-edit-everyone-sees-the-same-person", func() {
		r := ada.Lore(teamClone, "actor", "show", "--id", "github:"+oldLogin).OK(t)
		r.Says(t, pia.Name)
		r = ada.Lore(teamClone, "actor", "show", "--id", "github:"+newLogin).OK(t)
		r.Says(t, pia.Name)
		ada.Lore(folder, "task", "show", "--id", assignOld(t, "pia's access review")).OK(t).Says(t, pia.Name)
		ada.Lore(folder, "task", "show", "--id", taskID).OK(t).Says(t, pia.Name)
		ada.Lore(teamClone, "sync").OK(t)

		po := s.OwnerShaped(OwnerShape{Personal: journeys.Person{Name: "Pia Home", Email: "pia@home.example", Login: oldLogin}, Org: pia, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
		pwc := po.CloneTo(web, po.OrgDir("web"))
		r = po.Lore(pwc.Dir, "setup", "join", "--invite", team.SSHURL(), "--no-open").OK(t)
		oCheck(t, "D-961/join-prefills-new-login", strings.Contains(r.Out(), `"github_account":"`+newLogin+`"`) && !strings.Contains(r.Out(), `"github_account":"`+oldLogin+`"`),
			"the hire's join plan after member edit --github: want the new login %s prefilled, got:\n%s", newLogin, r)
		s.NamedFlags(t, r)
	})
}
