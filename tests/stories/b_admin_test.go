//go:build stories && !windows

package stories

// b_admin_test.go — story B (tests/stories/B-admin-people-walls.md): the
// admin of org acme runs the people side of a company for three weeks.
//
//	TestStoryBPeople   staff from a CSV (bad lines named), a new hire joins
//	                   by the store url and sees the company rule, a leaver's
//	                   open tasks are handed over; no store write pushes
//	                   before lore sync
//	TestStoryBSecrets  a fake token in a note is refused and never printed;
//	                   a code commit with one is blocked with the ready
//	                   `lore secrets allow --file …` line

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The company of story B (placeholders).
const (
	bOrg       = "acme"
	bSetup     = "acme"
	bProject   = "app"
	bStoreSlug = "github.com/acme/lore-team"
	bAppSlug   = "github.com/acme/app"
	bFolders   = "~/code/**"
	bRule      = "every service exposes a health endpoint"
	bTaskOne   = "move the billing cron to the queue"
	bTaskTwo   = "write the incident runbook"
)

var (
	bCTO   = journeys.Person{Name: "Cora Tate", Email: "cora@acme.example.test", Login: "cora-gh"}
	bLead  = journeys.Person{Name: "Lio Lead", Email: "lio@acme.example.test", Login: "lio-gh"}
	bHire  = journeys.Person{Name: "Nia Hale", Email: "nia@acme.example.test", Login: "nia-gh"}
	bLeave = journeys.Person{Name: "Ruth Gone", Email: "ruth@acme.example.test", Login: "ruth-gh"}
)

// bCompany is story B's world: the store, the app, each person's laptop and
// app clone (at ~/code/app, inside the setup's folders).
type bCompany struct {
	s     *Story
	store *teamharness.Remote
	app   *teamharness.Remote
	m     map[string]*journeys.Machine
	repo  map[string]*teamharness.Clone
}

func newBCompany(t *testing.T) *bCompany {
	s := New(t, "B")
	return &bCompany{s: s, store: s.Remote(bStoreSlug), app: s.Remote(bAppSlug),
		m: map[string]*journeys.Machine{}, repo: map[string]*teamharness.Clone{}}
}

// person gives p a laptop logged in to the fake GitHub with the app cloned.
func (b *bCompany) person(t *testing.T, p journeys.Person) *journeys.Machine {
	t.Helper()
	m := b.s.Person(p.Name, p.Email)
	journeys.GitHubLogin(t, b.s.World, m, p.Login)
	c := m.Clone(b.app, "app")
	m.MoveClone(t, c, filepath.Join(m.Home, "code", "app"))
	b.m[p.Email], b.repo[p.Email] = m, c
	return m
}

// lore runs lore in p's app clone.
func (b *bCompany) lore(p journeys.Person, args ...string) teamharness.Result {
	b.s.T.Helper()
	return b.m[p.Email].LoreIn(b.repo[p.Email], args...)
}

// start has the CTO start the org with its team store and link the app.
func (b *bCompany) start(t *testing.T) {
	t.Helper()
	cto := b.person(t, bCTO)
	journeys.StartTeam(t, cto, b.repo[bCTO.Email].Dir, bSetup, bOrg, b.store.SSHURL(), bCTO, []string{bFolders})
	b.lore(bCTO, "init").OK(t)
	b.lore(bCTO, "org", "link", bOrg, "--project", bProject, "--yes").OK(t)
	b.lore(bCTO, "sync").OK(t)
	c := b.repo[bCTO.Email]
	c.CommitAll("lore: set up the app")
	c.Git("push", "-q", "origin", "HEAD:main")
}

// TestStoryBPeople is the people side of the company's first three weeks.
func TestStoryBPeople(t *testing.T) {
	b := newBCompany(t)
	s := b.s
	var cto *journeys.Machine

	s.Step("day1/start-org", func() {
		b.start(t)
		cto = b.m[bCTO.Email]
	})

	s.Step("day1/staff-csv-with-bad-lines", func() {
		bad := filepath.Join(cto.Home, "staff-bad.csv")
		writeFile(t, bad, "email,name\n"+bLead.Email+",Lio\nnot-an-email,Broken\n"+bLead.Email+",Lio Again\n")
		r := b.lore(bCTO, "member", "add", "--from", bad, "--yes").Refused(t).
			Says(t, "nothing written", "line 3:", "line 4: "+bLead.Email+" is already on line 2")
		s.NamedFlags(t, r)
		b.lore(bCTO, "member", "list").OK(t).Never(t, bLead.Email)
	})

	s.Step("day1/staff-csv-no-push-before-sync", func() {
		csv := filepath.Join(cto.Home, "staff.csv")
		writeFile(t, csv, "email,name,handle,github,role,teams\n"+
			bLead.Email+","+bLead.Name+",lio,"+bLead.Login+",member,\n"+
			bHire.Email+","+bHire.Name+",nia,"+bHire.Login+",member,\n"+
			bLeave.Email+","+bLeave.Name+",ruth,"+bLeave.Login+",member,\n")
		refs := s.RefsOf(b.store)
		b.lore(bCTO, "member", "add", "--from", csv, "--yes").OK(t).Says(t, "added "+bLead.Email, "added "+bHire.Email, "added "+bLeave.Email)
		b.lore(bCTO, "rule", "add", "--to", bOrg+"/org", "--body", bRule).OK(t).Never(t, "git init")
		s.NoPushSince(t, b.store, refs, "member add --from and rule add --to <org>/org (D-43)")
		b.lore(bCTO, "sync").OK(t)
		if s.RefsOf(b.store) == refs {
			t.Error("lore sync did not send the roster and the rule to the team store")
		}
	})

	s.Step("day8/new-hire-joins-by-the-store-url", func() {
		s.AdvanceDays(7)
		nia := b.person(t, bHire)
		b.repo[bHire.Email].Git("pull", "-q", "origin", "main")
		journeys.Join(t, nia, b.repo[bHire.Email].Dir, bSetup, b.store.SSHURL(), bHire, []string{bFolders})
		b.lore(bHire, "whoami").OK(t).Says(t, bOrg, bHire.Email)
		b.lore(bHire, "rule", "list").OK(t).Says(t, bRule)
	})

	s.Step("day8/tasks-for-the-leaver", func() {
		b.lore(bCTO, "task", "add", "--title", bTaskOne, "--assign", "@ruth").OK(t)
		b.lore(bCTO, "task", "add", "--title", bTaskTwo, "--assign", "@ruth").OK(t)
		b.lore(bCTO, "task", "list", "--assignee", "@ruth").OK(t).Says(t, bTaskOne, bTaskTwo)
	})

	s.Step("day15/leaver-offboarded-work-handed-over", func() {
		s.AdvanceDays(7)
		r := b.lore(bCTO, "member", "remove", "--who", "@ruth", "--reason", "moved on", "--yes").OK(t).
			Says(t, "open tasks (2)", bTaskOne, bTaskTwo, "lore task reassign --from")
		s.NamedFlags(t, r)
		b.lore(bCTO, "task", "reassign", "--from", "@ruth", "--assign", "@nia", "--yes").OK(t).Says(t, "2 tasks reassigned to @nia")
		b.lore(bCTO, "task", "list", "--assignee", "@nia").OK(t).Says(t, bTaskOne, bTaskTwo)
		b.lore(bCTO, "member", "show", bLeave.Email).OK(t).Says(t, "left")
	})
}

// bAllowLine is one ready allow command a leak refusal prints.
var bAllowLine = regexp.MustCompile(`lore secrets allow --file \S+ --rule \S+ --match-hash sha256:[0-9a-f]+`)

// TestStoryBSecrets: a fake token pasted into a note never reaches a row or
// the screen; a code commit adding one is blocked by the hook, which prints
// the ready allow line and never the value.
func TestStoryBSecrets(t *testing.T) {
	b := newBCompany(t)
	s := b.s
	sec := FakeSecret("story-b")

	s.Step("day1/start-org", func() { b.start(t) })

	s.Step("day1/token-in-a-note", func() {
		r := b.lore(bCTO, "memory", "add", "--body", "the deploy bot uses "+sec+" for releases")
		if r.Code == 0 {
			t.Errorf("a note holding a token was saved as is: %s", r.Args)
		}
		s.NotPrinted(t, sec, r, b.lore(bCTO, "memory", "list"), b.lore(bCTO, "search", "--query", "deploy"), b.lore(bCTO, "check", "leaks", "--rows"))
		if out, err := bFilesHolding(t, b.repo[bCTO.Email].Dir, sec); err == nil && out != "" {
			t.Errorf("the token is on disk in the repo: %s", out)
		}
	})

	s.Step("day1/token-in-code-commit-blocked", func() {
		c := b.repo[bCTO.Email]
		c.WriteFile("deploy/env.yaml", "release:\n  token: \""+sec+"\"\n")
		c.Git("add", "-A")
		r := c.TryGit("commit", "-q", "-m", "release config").Refused(t, "E_LEAK_BLOCKED", "env.yaml")
		s.NotPrinted(t, sec, r)
		if len(bAllowLine.FindAllString(r.Out(), -1)) == 0 {
			t.Errorf("the refusal names no ready `lore secrets allow --file …` line:\n%s", r.Out())
		}
		s.NamedFlags(t, r)
	})
}

// bFilesHolding greps dir's files (outside .git) for needle and returns the files
// holding it.
func bFilesHolding(t *testing.T, dir, needle string) (string, error) {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		body, rerr := os.ReadFile(p)
		if rerr == nil && strings.Contains(string(body), needle) {
			hits = append(hits, p)
		}
		return nil
	})
	return strings.Join(hits, "\n"), err
}
