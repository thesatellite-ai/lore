//go:build stories && !windows

package stories

// BL_helpers_test.go — what the branch-layer stories (tests/stories/
// BL-branch-layers.md) need: one org with a team store, a client store and a
// client repo linked to a folder of it, and people who work on code
// branches there. Every name, email and url is made up.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The org of the branch-layer stories (placeholders).
const (
	blOrg       = "northwind"
	blSetup     = "northwind-admin"
	blJoinSetup = "northwind"
	blFolders   = "~/code/**"
	blFolder    = "client_app"
	blStoreSlug = "github.com/northwind-co/lore-team"
	blNotesSlug = "github.com/northwind-co/lore-client-app"
	blHomeSlug  = "github.com/northwind-co/platform"
	blAppSlug   = "github.com/contoso/app"
)

// The people of the branch-layer stories.
var (
	blAdmin = journeys.Person{Name: "Ada Admin", Email: "ada@example.test", Login: "ada-gh"}
	blDev   = journeys.Person{Name: "Dev Doer", Email: "dev@example.test", Login: "dev-gh"}
	blThird = journeys.Person{Name: "Tom Third", Email: "tom@example.test", Login: "tom-gh"}
)

// blWorld is one org, its stores and the linked client repo.
type blWorld struct {
	s      *Story
	notes  *teamharness.Remote // the client store
	app    *teamharness.Remote // the client's code repo
	admin  *journeys.Machine
	home   *teamharness.Clone // the admin's own repo (setup runs there)
	aapp   *teamharness.Clone // the admin's clone of the client repo
	people map[string]*journeys.Machine
	clones map[string]*teamharness.Clone
	team   bool // the @client_app team exists (blJoin)
}

// blStart starts the org, adds the client store and links the client repo
// to its folder, as story F does.
func blStart(s *Story) *blWorld {
	t := s.T
	t.Helper()
	w := &blWorld{s: s, people: map[string]*journeys.Machine{}, clones: map[string]*teamharness.Clone{}}
	team := s.Remote(blStoreSlug)
	w.notes = s.Remote(blNotesSlug)
	home := s.Seed(s.Remote(blHomeSlug), func(dir string) { PlainRepo(t, dir) })
	w.app = s.Seed(s.Remote(blAppSlug), func(dir string) { PlainRepo(t, dir) })
	w.admin, w.home = fPerson(s, blAdmin, home)
	journeys.StartTeam(t, w.admin, w.home.Dir, blSetup, blOrg, team.SSHURL(), blAdmin, []string{blFolders})
	w.aapp = fCodeClone(s, w.admin, w.app, "app")
	w.admin.LoreIn(w.home, "org", "store", "add", "--org", blOrg, "--url", w.notes.SSHURL(), "--yes").OK(t)
	w.admin.LoreIn(w.aapp, "org", "link", "--org", blOrg, "--project", blFolder, "--store", w.notes.SSHURL(), "--yes").OK(t).Says(t, "linked")
	// a first note on the default branch, so the folder has shared notes
	w.admin.LoreIn(w.aapp, "memory", "add", "--body", "the checkout runs on port 8080").OK(t)
	w.admin.LoreIn(w.aapp, "sync").OK(t)
	return w
}

// blJoin invites p, gives them a laptop with the client repo cloned and
// joins them to the org.
func (w *blWorld) blJoin(p journeys.Person) (*journeys.Machine, *teamharness.Clone) {
	t := w.s.T
	t.Helper()
	_, line := journeys.Invite(t, w.admin, w.home.Dir, p.Email)
	m := w.s.Persona(p.Name, p.Email).Laptop()
	journeys.GitHubLogin(t, w.s.World, m, p.Login)
	c := fCodeClone(w.s, m, w.app, "app")
	journeys.Join(t, m, c.Dir, blJoinSetup, line, p, []string{blFolders})
	// devco D-694: a member adds notes only in the folder of a team they
	// are on; every BL person writes for the team in client_app, so the
	// admin puts them on @client_app (the team named like the folder)
	if w.team {
		w.admin.LoreIn(w.home, "team", "member", "add", "--handle", "@"+blFolder, "--members", p.Email, "--yes").OK(t)
	} else {
		w.admin.LoreIn(w.home, "team", "add", "--handle", "@"+blFolder, "--members", p.Email, "--yes").OK(t)
		w.team = true
	}
	w.admin.LoreIn(w.home, "sync").OK(t)
	m.LoreIn(c, "sync").OK(t)
	w.people[p.Email], w.clones[p.Email] = m, c
	return m, c
}

// blTaskID matches a task id in an output.
var blTaskID = regexp.MustCompile(`tsk_[0-9a-f]{32}`)

// blMemID matches a memory id in an output.
var blMemID = regexp.MustCompile(`mem_[0-9a-f]{32}`)

// blRuleID matches a rule id in an output.
var blRuleID = regexp.MustCompile(`rul_[0-9a-f]{32}`)

// idIn returns the first match of re in r's output.
func idIn(t testing.TB, re *regexp.Regexp, r teamharness.Result) string {
	t.Helper()
	id := re.FindString(r.Stdout + r.Stderr)
	if id == "" {
		t.Fatalf("no id in:\n%s", r)
	}
	return id
}

// storeRefs lists the hidden layer refs on the client store's remote.
func (w *blWorld) storeRefs(prefix string) string {
	w.s.T.Helper()
	return strings.TrimSpace(w.s.TryGit(w.notes.Dir, "for-each-ref", "--format=%(refname)", prefix).Stdout)
}
