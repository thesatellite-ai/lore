//go:build stories && !windows

package stories

// F_helpers_test.go — what story F (client repo, contractor, CI, people)
// needs on top of the shared harness: one org started through the guided
// setup the way an admin saves it on the page, people with their own GitHub
// login, and the checks a client repo must pass. Every name, email and url
// is made up.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The org of story F (placeholders).
const (
	fOrg       = "fabrikam"
	fSetup     = "fabrikam-admin"
	fJoinSetup = "fabrikam"
	fFolders   = "~/code/**"
	fClientDir = "client_webshop"
	fStoreSlug = "github.com/fabrikam-co/lore-team"
	fAppSlug   = "github.com/fabrikam-co/platform"
	fShopSlug  = "github.com/shopco/webshop"
	fPHPSlug   = "github.com/fabrikam-co/billing-php"
)

// The people of story F: the CTO is the org admin; the others join.
var (
	fCTO     = journeys.Person{Name: "Cleo Admin", Email: "cleo@example.test", Login: "cleo-gh"}
	fLead    = journeys.Person{Name: "Lars Lead", Email: "lars@example.test", Login: "lars-gh"}
	fNewhire = journeys.Person{Name: "Nia Newhire", Email: "nia@example.test", Login: "nia-gh"}
	fLeaver  = journeys.Person{Name: "Leo Leaving", Email: "leo@example.test", Login: "leo-gh"}
)

// fTaskID matches the task id task add prints.
var fTaskID = regexp.MustCompile(`(tsk_[0-9a-f]{32})`)

// fActorID matches an actor id anywhere in a file or an output.
var fActorID = regexp.MustCompile(`act_[0-9a-f]{8,}`)

// fPerson gives p a laptop with their GitHub login and the internal repo
// cloned at ~/code/platform, inside the setup's folders (the setup only
// governs folders it names).
func fPerson(s *Story, p journeys.Person, app *teamharness.Remote) (*journeys.Machine, *teamharness.Clone) {
	s.T.Helper()
	m := s.Persona(p.Name, p.Email).Laptop()
	journeys.GitHubLogin(s.T, s.World, m, p.Login)
	c := m.Clone(app, "platform")
	m.MoveClone(s.T, c, filepath.Join(m.Home, "code", "platform"))
	return m, c
}

// fCodeClone clones r into ~/code/<name> on m (inside the setup's folders).
func fCodeClone(s *Story, m *journeys.Machine, r *teamharness.Remote, name string) *teamharness.Clone {
	s.T.Helper()
	c := m.Clone(r, name)
	m.MoveClone(s.T, c, filepath.Join(m.Home, "code", name))
	return c
}

// fTaskOf returns the task id an add printed.
func fTaskOf(t testing.TB, r teamharness.Result) string {
	t.Helper()
	m := fTaskID.FindStringSubmatch(r.Stdout)
	if m == nil {
		t.Fatalf("task add printed no task id:\n%s", r)
	}
	return m[1]
}

// fNoLoreIn fails when the client clone holds anything of lore's: a .lore
// folder, a lore hook, lore's merge driver, or any untracked or ignored file
// (git status --ignored is what the client would see and could commit).
func fNoLoreIn(t testing.TB, c *teamharness.Clone, what string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(c.Dir, ".lore")); err == nil {
		t.Errorf("%s: the client repo has a .lore folder", what)
	}
	if cfg := c.Git("config", "--local", "--list"); strings.Contains(cfg, "merge.lore") {
		t.Errorf("%s: lore's merge driver is set in the client clone:\n%s", what, cfg)
	}
	hook := strings.TrimSpace(c.Git("rev-parse", "--path-format=absolute", "--git-path", "hooks/pre-commit"))
	if b, err := os.ReadFile(hook); err == nil && strings.Contains(string(b), "lore") {
		t.Errorf("%s: lore's hooks are installed in the client clone (%s)", what, hook)
	}
	if st := c.Git("status", "--porcelain", "--ignored"); st != "" {
		t.Errorf("%s: the client repo must hold nothing of lore's, git status:\n%s", what, st)
	}
}

// fNothingInternal fails when the client remote's whole history (every
// object) holds an internal detail: an actor id, an internal email, the
// org's name or its store address.
func fNothingInternal(t testing.TB, r *teamharness.Remote, needles ...string) {
	t.Helper()
	if hits := r.AllObjectsContain(needles...); len(hits) != 0 {
		t.Errorf("the client remote's history holds internal details: %v", hits)
	}
}

// fReadOnly takes write permission off every folder under dir (on) or gives
// it back (off): a CI runner whose HOME the job cannot write (chmod a-w).
func fReadOnly(t testing.TB, dir string, on bool) {
	t.Helper()
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() {
			return err
		}
		mode := fi.Mode().Perm() | 0o200
		if on {
			mode = fi.Mode().Perm() &^ 0o222
		}
		return os.Chmod(p, mode)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fNoIdentity drops the git identity from m's HOME (a CI runner has none).
func fNoIdentity(s *Story, m *journeys.Machine) {
	gc := filepath.Join(m.Home, ".gitconfig")
	for _, k := range []string{"user.name", "user.email"} {
		s.TryGit(m.Home, "config", "--file", gc, "--unset", k)
	}
}
