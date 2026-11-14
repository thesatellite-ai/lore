//go:build journeys && !windows

package journeys

// staff_test.go — adding the org's own staff (owner report 2026-11-12:
// "those are part of the company already, so why invite"). Lee adds three
// people from a CSV as members in one store commit, with org roles and
// teams; Ann joins on her own laptop by the org's store url, matched by
// her email; someone not on the roster is refused with the admin's
// command; an outsider still joins with an invite line.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyStaff(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	mainRef := "refs/heads/main"

	w.Step("day1/start-team", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "team", "add", "@web", "--members", "@lee").OK(t)
		tt.lore(tlLee, "team", "add", "@ops", "--members", "@lee").OK(t)
	})

	w.Step("day1/a-bad-csv-names-every-line", func() {
		// B-2: one run names every bad line by its file line, a duplicate
		// names both lines, a broken quote says why; nothing is written
		bad := filepath.Join(lee.Home, "staff-bad.csv")
		jWrite(t, bad, "email,name\n"+tlAnn.Email+",Ann\nnot-an-email,Broken\n"+tlAnn.Email+",Ann Again\n")
		tt.lore(tlLee, "member", "add", "--from", bad, "--yes").Refused(t).
			Says(t, "2 lines need a fix, nothing written", `line 3: "not-an-email" is not an email address`,
				"line 4: "+tlAnn.Email+" is already on line 2", "in staff-bad.csv fix line 3", "--from "+bad+" --dry-run")
		quote := filepath.Join(lee.Home, "quote.csv")
		jWrite(t, quote, "email,name\n\""+tlAnn.Email+",broken\n")
		tt.lore(tlLee, "member", "add", "--from", quote, "--dry-run").Refused(t).Says(t, "line 2: a quote", "never closed", "close the quote")
		tt.lore(tlLee, "member", "list").OK(t).Never(t, tlAnn.Email)
	})

	w.Step("day1/add-staff-from-a-csv", func() {
		csv := filepath.Join(lee.Home, "staff.csv")
		jWrite(t, csv, "email,name,handle,github,role,teams\n"+
			tlAnn.Email+","+tlAnn.Name+",ann,"+tlAnn.Login+",member,@web\n"+
			tlBob.Email+","+tlBob.Name+",bob,"+tlBob.Login+",pm,@web;@ops\n"+
			tlCat.Email+","+tlCat.Name+",cat,,member,@ops\n")
		tt.lore(tlLee, "member", "add", "--from", csv, "--dry-run").OK(t).Says(t, "would add "+tlAnn.Email, "nothing written")
		tt.lore(tlLee, "sync").OK(t) // send the team adds first, so the count is member add's alone
		before := tt.store.CommitCount(mainRef)
		tt.lore(tlLee, "member", "add", "--from", csv, "--yes").OK(t).
			Says(t, "added "+tlAnn.Email, "added "+tlBob.Email, "role pm", "one store commit", "once the roster is sent (`lore sync`)", "lore setup join",
				"committed locally to store").Never(t, "pushed store")
		if n := tt.store.CommitCount(mainRef) - before; n != 0 {
			t.Errorf("member add pushed %d store commits before Lee asked (C5)", n)
		}
		tt.lore(tlLee, "sync").OK(t).Says(t, "pushed store")
		if n := tt.store.CommitCount(mainRef) - before; n != 1 {
			t.Errorf("member add --from wrote %d store commits, want ONE for the three people", n)
		}
		// a second run adds nobody
		tt.lore(tlLee, "member", "add", "--from", csv, "--yes").OK(t).Says(t, "already a member: "+tlAnn.Email, "nothing to add")
		tt.lore(tlLee, "member", "list").OK(t).Says(t, tlAnn.Email, tlBob.Email, tlCat.Email, "pm")
		tt.lore(tlLee, "member", "show", tlBob.Email).OK(t).Says(t, "role:", "pm", "@ops", "@web")
		tt.lore(tlLee, "team", "show", "@web").OK(t).Says(t, "members (3)", tlAnn.Name, tlBob.Name).Never(t, "pending")
		tt.lore(tlLee, "team", "show", "@ops").OK(t).Says(t, "members (3)", tlBob.Name, tlCat.Name)
		// a pending invite written before member add existed converts
		tlInvite(t, lee, tt.repo[tlLee.Email].Dir, "eve@example.test", "--team", "@web")
		tt.lore(tlLee, "doctor").Says(t, "pending_invites", "eve@example.test", "lore member add --from-invites")
		tt.lore(tlLee, "member", "add", "--from-invites", "--yes").OK(t).Says(t, "added eve@example.test", "now accepted", "teams @web")
		tt.lore(tlLee, "team", "invite", "list").OK(t).Says(t, "eve@example.test", "accepted")
		tt.lore(tlLee, "team", "show", "@web").OK(t).Says(t, "members (4)").Never(t, "pending")
		// whoever started the org is its admin; an org from before gets it
		// from doctor --fix (org_admin_missing)
		tt.lore(tlLee, "member", "show", tlLee.Email).OK(t).Says(t, "role:", "admin")
		tt.lore(tlLee, "actor", "period", "edit", "@lee", "--clear", "org_role", "--yes").OK(t)
		tt.lore(tlLee, "doctor", "--fresh").Says(t, "org_admin_missing", tlLee.Name, "lore doctor --fix")
		tt.lore(tlLee, "doctor", "--fix", "--yes").Says(t, "org_admin_missing")
		tt.lore(tlLee, "doctor", "--fresh").Never(t, "org_admin_missing")
		tt.lore(tlLee, "member", "show", tlLee.Email).OK(t).Says(t, "admin")
		// a handle with capitals is lowercased and said so; the GitHub login
		// is kept as given; a handle is derived when none is given
		tt.lore(tlLee, "member", "add", "onur@example.test", "--name", "Onur", "--handle", "@OnurFromAgency", "--github", "OnurFromAgency", "--yes").OK(t).
			Says(t, "handle @OnurFromAgency is stored as @onurfromagency")
		tt.lore(tlLee, "member", "show", "onur@example.test").OK(t).Says(t, "@onurfromagency", "OnurFromAgency")
		tt.lore(tlLee, "member", "add", "Zed.Ops@example.test", "--github", "ZedOps", "--yes").OK(t).Says(t, "handle @zedops (from the GitHub login")
		tt.lore(tlLee, "member", "edit", "onur@example.test", "--handle", "!!!", "--yes").Refused(t).Says(t, "person handle")
		tt.lore(tlLee, "member", "edit", "onur@example.test", "--handle", "Onur Dev", "--yes").OK(t).Says(t, "stored as @onur-dev")
		// every write takes --yes
		tt.lore(tlLee, "team", "member", "add", "@ops", "@onur-dev", "--yes").OK(t)
		// roles change without a project
		tt.lore(tlLee, "member", "edit", tlCat.Email, "--role", "pm", "--yes").OK(t).Says(t, "updated")
		tt.lore(tlLee, "member", "show", tlCat.Email).OK(t).Says(t, "pm")
	})

	w.Step("day2/ann-joins-by-email", func() {
		// C5: Lee sends the day's roster writes before Ann joins
		tt.lore(tlLee, "sync").OK(t)
		w.AdvanceDays(1)
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		tlFirstSteps(t, ann, dir, tlTeamSetup, "member", tlAnn)
		tlApply(t, ann, dir, tlTeamSetup, "join", map[string]any{"invite": tt.store.SSHURL()})
		tlLastSteps(t, ann, dir, tlTeamSetup, []string{tlFolders})
		tt.lore(tlAnn, "whoami").OK(t).Says(t, tlOrg, tlAnn.Email)
		// Ann's clone has no lore project (the join writes only to the org
		// store): the roster commands read the org store there; a general
		// read such as `lore actor list` says lore is not set up in it
		tt.lore(tlAnn, "team", "show", "@web").OK(t).Says(t, tlAnn.Name)
		tt.lore(tlAnn, "member", "show", tlAnn.Email).OK(t).Says(t, tlAnn.Name, "active")
		if a := tt.roster(t, tlLee)["human:"+tlAnn.Email]; a.Display != tlAnn.Name || a.Status != "active" {
			t.Errorf("Ann's roster entry after her join: %+v", a)
		}
	})

	w.Step("day2/someone-not-on-the-roster", func() {
		dan := tt.person(tlDan)
		dir := tt.repo[tlDan.Email].Dir
		tlFirstSteps(t, dan, dir, tlTeamSetup, "member", tlDan)
		r := dan.Lore(dir, "setup", "apply", tlTeamSetup, "--step", "join", "--answers", `{"invite":"`+tt.store.SSHURL()+`"}`, "--json")
		if r.Code == 0 || !strings.Contains(r.Out(), "is not a member of") || !strings.Contains(r.Out(), "lore member add --email "+tlDan.Email) {
			t.Errorf("a join by someone not on the roster: %s", r)
		}
		// an outsider still joins with an invite line
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlDan.Email, "--team", "@ops")
		tt.lore(tlLee, "sync").OK(t) // C5: the invite reaches the team store with Lee's sync
		tlApply(t, dan, dir, tlTeamSetup, "join", map[string]any{"invite": line})
		tlLastSteps(t, dan, dir, tlTeamSetup, []string{tlFolders})
		tt.lore(tlDan, "sync").OK(t) // C5: Dan's join reaches the team with his sync
		tt.lore(tlLee, "sync", "run").OK(t)
		tt.lore(tlLee, "team", "show", "@ops").OK(t).Says(t, tlDan.Name)
		tt.lore(tlLee, "team", "list").OK(t).Says(t, "@web", "@ops")
	})
}
