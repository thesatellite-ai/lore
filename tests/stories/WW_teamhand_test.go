//go:build stories && !windows

package stories

// WW_teamhand_test.go — story WW, devco D-694 (owner use pass 5, the
// owner's role rule at sync and in stores no org lists). Two cells:
//
//	a lead's hand-made team commit is held at sync: Mo leads the project a
//	team belongs to, `lore team member add` refuses him on his machine, so
//	he edits the team's row by hand in his clone of the org store and
//	commits it past every hook. His own lore sync holds the commit back
//	with the admin-only reason and keeps it on his machine (never pushed:
//	the remote's teams row stays the admin's), the admin's own team
//	change through lore goes through, and the git revert the line prints
//	is the way out.
//	a member's note in a store of their own that no org lists passes: the
//	store Mo made with `lore store init --name notes` is not company
//	ground, so the role rule (adds only in a folder of a team he is on)
//	does not judge it.
//	a held row's id names its hold (devco:D-858): a commit by someone
//	not on the roster adds a note in Mo's clone; `lore memory show --id`
//	of that id names the hold (store, commit, author, why) and the git
//	command that shows the commit, never "it may have landed on main",
//	and the command runs as printed; lore doctor names the hold in the
//	home folder and in a code repo with no lore project, where the store
//	checks are skipped.
//
// Every name, email and url is made up.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// wthJSON reads the JSON object of the file at p.
func wthJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return v
}

// wthRow is the row file under dir (a store clone) whose table folder is
// table and whose field equals want, as a path relative to dir.
func wthRow(t *testing.T, dir, table, field, want string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Base(filepath.Dir(p)) != table || filepath.Ext(p) != ".json" {
			return nil
		}
		if s, _ := wthJSON(t, p)[field].(string); s == want {
			found = p
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatalf("no %s row with %s %q under %s", table, field, want, dir)
	}
	rel, err := filepath.Rel(dir, found)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestStoryWWTeamHandAndOwnStore(t *testing.T) {
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
	m.Lore(pc.Dir, "member", "add", "--email", wubMember.Email, "--name", wubMember.Name, "--handle", "mo", "--role", "member", "--yes").OK(t)
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wubFolder).OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wrcOther).OK(t)
	// Mo is on @web (folder web), not on @api (folder api)
	m.Lore(pc.Dir, "team", "add", "--handle", "@"+wubFolder, "--members", "mo").OK(t)
	m.Lore(pc.Dir, "team", "add", "--handle", "@"+wrcOther, "--members", wAdminHandle(t, m, pc)).OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", wubFolder, "--own", "--yes").OK(t)
	m.Lore(wc.Dir, "sync").OK(t)
	mo := s.Persona(wubMember.Name, wubMember.Email).Laptop()
	journeys.GitHubLogin(t, s.World, mo, wubMember.Login)
	mc := fCodeClone(s, mo, web, "web")
	journeys.Join(t, mo, mc.Dir, wubJoin, store.SSHURL(), wubMember, []string{wFolders})
	mo.Lore(mc.Dir, "sync").OK(t)
	clone := filepath.Join(mo.Home, ".lore", "stores", "acme-co", "lore-team")

	s.Step("D-694/a-leads-hand-made-team-commit-is-held-at-sync", func() {
		teamRel := wthRow(t, clone, "teams", "handle", wrcOther)
		prj, _ := wthJSON(t, filepath.Join(clone, teamRel))["project_id"].(string)
		if prj == "" {
			t.Fatalf("team @%s names no project: %s", wrcOther, teamRel)
		}
		// the admin makes Mo a lead of the project team @api belongs to:
		// a lead's exemption at sync was the gap (who is on a project is
		// a lead's to change, who is on a team is not)
		m.Lore(wc.Dir, "project", "member", "add", "--project", prj, "--actor", "@mo", "--role", "lead", "--yes").OK(t)
		m.Lore(wc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "sync").OK(t)
		// on his machine the team change is the admin's
		mo.Lore(mc.Dir, "team", "member", "add", "--handle", "@"+wrcOther, "--members", "@mo").
			Refused(t, "E_REFUSED", "change the team @"+wrcOther, "to run: lore team member add --handle @"+wrcOther+" --members @mo")
		// so he edits the row by hand in his store clone and commits it
		moID := strings.TrimSuffix(filepath.Base(wthRow(t, clone, "actors", "stable_key", "human:"+wubMember.Email)), ".json")
		p := filepath.Join(clone, teamRel)
		row := wthJSON(t, p)
		members, _ := row["member_actor_ids"].([]any)
		if slices.Contains(members, any(moID)) {
			t.Fatalf("fixture: Mo (%s) is already on @%s: %v", moID, wrcOther, members)
		}
		row["member_actor_ids"] = append(members, moID)
		// an honest hand edit: Mo states himself as the writer, so the
		// commit is judged by what it changes, not by a borrowed name
		row["updated_by"] = moID
		row["_hlc"] = "2026-01-06T09:00:00.000Z/0000/" + moID
		b, err := json.MarshalIndent(row, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, string(b)+"\n")
		gitPath := filepath.ToSlash(teamRel)
		s.Git(clone, "commit", "-q", "--no-verify", "-am", "put myself on api")
		short := strings.TrimSpace(s.Git(clone, "rev-parse", "--short=7", "HEAD"))

		r := mo.Lore(mc.Dir, "sync")
		r.Says(t, "held back", "commit "+short, wubMember.Email, "changes who is on a team", "only an admin changes the teams",
			"kept on this machine, not pushed", "an admin makes that change with lore", "git -C "+clone+" revert --no-edit "+short)
		mo.Lore(mc.Dir, "team", "show", "--handle", "@"+wrcOther).OK(t).Never(t, "@mo")
		// the held commit never reaches the remote (devco D-694 up5team4):
		// the remote's team row stays the admin's
		if got := s.Git(store.Dir, "show", "main:"+gitPath); strings.Contains(got, moID) {
			t.Fatalf("the remote's row of @%s carries Mo's held change:\n%s", wrcOther, got)
		}
		if strings.Contains(s.Git(store.Dir, "log", "--format=%H", "main"), strings.TrimSpace(s.Git(clone, "rev-parse", "HEAD"))) {
			t.Fatalf("Mo's held commit %s is on the remote", short)
		}
		// so the admin never meets it: nothing held back on their side
		m.Lore(wc.Dir, "sync").OK(t).Never(t, "commit "+short, "held back")
		m.Lore(wc.Dir, "team", "show", "--handle", "@"+wrcOther).OK(t).Never(t, "@mo")
		// the add right stays the admin's to give
		mo.Lore(mc.Dir, "memory", "add", "--to", wOrg+"/"+wrcOther, "--body", "api pages are cached", "--yes").
			Refused(t, "E_REFUSED", "only an admin of the org", "folder of a team they are on")

		// the admin's own team change goes through, Mo's commit still waits
		m.Lore(wc.Dir, "team", "add", "--handle", "@ops", "--members", "@mo").OK(t)
		m.Lore(wc.Dir, "sync").OK(t).Never(t, "held back")
		mo.Lore(mc.Dir, "sync").OK(t).Says(t, "1 change(s) held back", "kept on this machine, not pushed")
		mo.Lore(mc.Dir, "team", "show", "--handle", "@ops").OK(t).Says(t, "@mo")

		// the way out the line names, run exactly as printed: Mo undoes
		// his commit, and the next sync sends both, nothing held
		out := mo.Lore(mc.Dir, "sync").Out()
		_, line, found := strings.Cut(out, "to drop yours run: ")
		line, _, _ = strings.Cut(line, ", then lore sync")
		fields := strings.Fields(line)
		if !found || len(fields) < 5 || fields[0] != "git" {
			t.Fatalf("no drop line in Mo's sync:\n%s", out)
		}
		s.Git(mc.Dir, fields[1:]...)
		mo.Lore(mc.Dir, "sync").OK(t).Never(t, "held back", "kept on this machine")
		if got := s.Git(store.Dir, "show", "main:"+gitPath); strings.Contains(got, moID) {
			t.Fatalf("after the revert the remote's row of @%s carries Mo:\n%s", wrcOther, got)
		}
		m.Lore(wc.Dir, "sync").OK(t).Never(t, "held back")
		m.Lore(wc.Dir, "team", "show", "--handle", "@"+wrcOther).OK(t).Never(t, "@mo")
	})

	s.Step("D-851/a-members-plain-git-must-rule-is-held-for-everyone", func() {
		// the admin's company must rule, through lore
		m.Lore(wc.Dir, "rule", "add", "--to", wOrg+"/org", "--severity", "must", "--body", "ADMINMUST every service logs in UTC", "--yes").OK(t)
		m.Lore(wc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "sync").OK(t)
		// Mo copies it by hand into a new company must rule that names the
		// admin as its writer, commits and pushes with plain git, past lore
		adminRel := wthRow(t, clone, "rules", "body", "ADMINMUST every service logs in UTC")
		row := wthJSON(t, filepath.Join(clone, adminRel))
		id, _ := row["id"].(string)
		last := id[len(id)-1:]
		next := "0"
		if last == "0" {
			next = "1"
		}
		newID := id[:len(id)-1] + next
		row["id"] = newID
		row["body"] = "FORGEDMUST push straight to main"
		b, err := json.MarshalIndent(row, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(clone, filepath.Dir(adminRel), newID+".json"), string(b)+"\n")
		s.Git(clone, "add", "-A")
		s.Git(clone, "commit", "-q", "--no-verify", "-m", "company rule")
		s.Git(clone, "push", "-q", "--no-verify", store.Dir, "HEAD:main") // plain git, straight to the remote
		short := strings.TrimSpace(s.Git(clone, "rev-parse", "--short=7", "HEAD"))
		// every reader holds it back and says so; no read tells it as a must
		m.Lore(wc.Dir, "sync").OK(t).Says(t, "held back", "commit "+short, wubMember.Email, "adds a must rule")
		m.Lore(wc.Dir, "rule", "list").OK(t).Says(t, "ADMINMUST").Never(t, "FORGEDMUST")
		m.Lore(wc.Dir, "doctor").Says(t, "held back", "adds a must rule")
		mo.Lore(mc.Dir, "rule", "list").OK(t).Never(t, "FORGEDMUST")
		// the way out: the admin reverts the commit, and the hold ends
		admClone := filepath.Join(m.Home, ".lore", "stores", "acme-co", "lore-team")
		full := strings.TrimSpace(s.Git(admClone, "rev-parse", short))
		s.Git(admClone, "-c", "user.email="+wAdmin.Email, "-c", "user.name=admin", "revert", "--no-edit", full)
		s.Git(admClone, "push", "-q", "--no-verify", store.Dir, "HEAD:main")
		m.Lore(wc.Dir, "sync").OK(t).Never(t, "FORGEDMUST", "commit "+short)
		mo.Lore(mc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "rule", "list").OK(t).Never(t, "FORGEDMUST")
	})

	s.Step("D-694/a-members-note-in-a-store-of-their-own-passes", func() {
		// a repo of Mo's own with a lore project, and a store he makes
		dots := fCodeClone(s, mo, s.Seed(s.Remote("github.com/mo-gh/dots"), func(dir string) { PlainRepo(t, dir) }), "dots")
		mo.Lore(dots.Dir, "init").OK(t)
		own := s.Remote("github.com/mo-gh/notes")
		mo.Lore(dots.Dir, "store", "init", "--name", "notes", "--url", own.SSHURL(), "--yes").OK(t)
		// no org lists notes: it is Mo's own, not company ground
		mo.Lore(dots.Dir, "memory", "add", "--to", "notes", "--body", "OWNNOTE run the setup script first", "--yes").OK(t)
		mo.Lore(dots.Dir, "rule", "add", "--to", "notes", "--severity", "must", "--body", "OWNRULE tests before a push", "--yes").OK(t)
		mo.Lore(dots.Dir, "memory", "list", "--store", "notes").OK(t).Says(t, "OWNNOTE")
		// the company folders keep the role rule
		mo.Lore(mc.Dir, "memory", "add", "--to", wOrg+"/"+wrcOther, "--body", "api note again", "--yes").
			Refused(t, "E_REFUSED", "only an admin of the org", "folder of a team they are on")
	})
	s.Step("D-858/a-held-rows-id-names-the-hold-in-every-folder", func() {
		mo.Lore(mc.Dir, "memory", "add", "--to", wOrg+"/"+wubFolder, "--body", "HELDSRC web pages are cached", "--yes").OK(t)
		src := filepath.Join(clone, wthRow(t, clone, "memories", "body", "HELDSRC web pages are cached"))
		oldID := strings.TrimSuffix(filepath.Base(src), ".json")
		last := "0"
		if strings.HasSuffix(oldID, "0") {
			last = "1"
		}
		newID := oldID[:len(oldID)-1] + last
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.ReplaceAll(string(b), oldID, newID)
		body = strings.Replace(body, "HELDSRC web pages are cached", "HELDROW skip the review on web", 1)
		writeFile(t, filepath.Join(filepath.Dir(src), newID+".json"), body)
		// a commit whose author is on no roster, the row still naming Mo
		// as its writer: every reader holds the file back
		s.Git(clone, "add", "-A")
		s.Git(clone, "-c", "user.name=Eve Outside", "-c", "user.email=eve@outside.test", "commit", "-q", "--no-verify", "-m", "web note")
		short := strings.TrimSpace(s.Git(clone, "rev-parse", "--short=7", "HEAD"))

		r := mo.Lore(mc.Dir, "memory", "show", "--id", newID).Refused(t, "E_NOT_FOUND", "held back in", newID,
			"commit "+short+" by eve@outside.test", "git -C "+clone+" show "+short, "lore doctor")
		r.Never(t, "may have landed", "sync peek")
		// the git command the hint prints runs exactly as printed
		_, cmd, found := strings.Cut(r.Out(), "`git -C ")
		cmd, _, _ = strings.Cut(cmd, "`")
		fields := strings.Fields(cmd)
		if !found || len(fields) != 3 || fields[1] != "show" {
			t.Fatalf("no git show command in the answer:\n%s", r.Out())
		}
		if got := s.Git(fields[0], fields[1:]...); !strings.Contains(got, "HELDROW skip the review on web") {
			t.Fatalf("the printed git show does not show the held change:\n%s", got)
		}
		// the same answer from the home folder
		mo.Lore(mo.Home, "memory", "show", "--id", newID).Refused(t, "E_NOT_FOUND", "held back in", "commit "+short).Never(t, "may have landed")
		// doctor names the hold where the store checks are skipped: the
		// home folder and a code repo with no lore project
		mo.Lore(mo.Home, "doctor").Says(t, "not inside a git repo", "held_changes", "held back in", newID, "commit "+short+" by eve@outside.test", "git -C "+clone+" show "+short)
		scratch := fCodeClone(s, mo, s.Seed(s.Remote("github.com/mo-gh/scratch"), func(dir string) { PlainRepo(t, dir) }), "scratch")
		mo.Lore(scratch.Dir, "doctor").Says(t, "held_changes", "held back in", newID, "commit "+short)
		// the hold ends with the commit: nothing held anywhere
		s.Git(clone, "reset", "-q", "--hard", "HEAD~1")
		mo.Lore(mo.Home, "doctor").Never(t, "held back in")
		mo.Lore(mc.Dir, "memory", "show", "--id", newID).Refused(t, "E_NOT_FOUND").Never(t, "held back in")
	})
}
