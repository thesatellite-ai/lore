//go:build stories && !windows

package stories

// WW_teamhand_test.go — story WW, devco D-694 (owner use pass 5, the
// owner's role rule at sync and in stores no org lists). Two cells:
//
//	a lead's hand-made team commit is held at sync: Mo leads the project a
//	team belongs to, `lore team member add` refuses him on his machine, so
//	he edits the team's row by hand in his clone of the org store and
//	commits it past every hook. His own lore sync holds the commit back
//	with the admin-only reason, the team stays as the admin made it for
//	everyone (the remote's teams row included), and the admin's own team
//	change through lore goes through.
//	a member's note in a store of their own that no org lists passes: the
//	store Mo made with `lore store init --name notes` is not company
//	ground, so the role rule (adds only in a folder of a team he is on)
//	does not judge it.
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
		r.Says(t, "held back", "commit "+short, wubMember.Email, "changes who is on a team", "only an admin changes the teams")
		mo.Lore(mc.Dir, "team", "show", "--handle", "@"+wrcOther).OK(t).Never(t, "@mo")
		// sync still sends the commit (the remote's row file carries Mo);
		// every reader's lore holds it back, so for everyone the team is
		// the one the admin made (see the LEFT of devco D-694 up5team3)
		if !strings.Contains(s.Git(store.Dir, "show", "main:"+gitPath), moID) {
			t.Logf("devco:D-694: the held team commit stayed on Mo's machine")
		}
		m.Lore(wc.Dir, "sync").OK(t).Says(t, "held back in "+wOrg, "commit "+short, "only an admin changes the teams", "1 change(s) held back")
		m.Lore(wc.Dir, "team", "show", "--handle", "@"+wrcOther).OK(t).Never(t, "@mo")
		// the add right stays the admin's to give
		mo.Lore(mc.Dir, "memory", "add", "--to", wOrg+"/"+wrcOther, "--body", "api pages are cached", "--yes").
			Refused(t, "E_REFUSED", "only an admin of the org", "folder of a team they are on")

		// the admin's own team change goes through
		m.Lore(wc.Dir, "team", "add", "--handle", "@ops", "--members", "@mo").OK(t)
		// read by everyone: still the one held change (Mo's), never the admin's
		m.Lore(wc.Dir, "sync").OK(t).Says(t, "1 change(s) held back")
		mo.Lore(mc.Dir, "sync").OK(t).Says(t, "1 change(s) held back")
		mo.Lore(mc.Dir, "team", "show", "--handle", "@ops").OK(t).Says(t, "@mo")
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
}
