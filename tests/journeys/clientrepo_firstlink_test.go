//go:build journeys && !windows

package journeys

// clientrepo_firstlink_test.go — devco:D-733: Mira's agency lists only
// its own code under own: (github.com/agency-co/*); a client's repo is
// listed nowhere until she links it. In a fresh clone of that repo the
// FIRST `lore org link` installed no hook: the run kept the org answer it
// gathered before the link was written (no link yet, so not a client
// repo), and the guard installed after a successful link read that
// answer. whoami then said "guards OFF … lore org link … here installs
// it", and only a second identical link installed the hook. Now the one
// link installs commit-msg and pre-push in .git/hooks, whoami says the
// guards are on, and a commit that repeats an internal rule is refused.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	flOrg    = "agency"
	flOwn    = "github.com/agency-co/*"
	flClient = "github.com/client-co/payroll"
	flFolder = "client_payroll"
	flRule   = "Always reconcile the payroll exports against the nightly settlement batch first."
)

func TestJourneyClientRepoFirstLinkGuards(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Castell", "mira@agency.example").Laptop()
	store := w.Remote("github.com/agency-co/lore-store")
	tools := jPlainRepo(t, mira, w.Remote("github.com/agency-co/tools"), filepath.Join(mira.Home, "work", "tools"))
	app := jPlainRepo(t, mira, w.Remote(flClient), filepath.Join(mira.Home, "work", "payroll"))

	w.Step("day1/org-lists-only-its-own-repos", func() {
		mira.LoreIn(tools, "store", "init", "--name", "agencystore", "--url", store.SSHURL()).OK(t)
		mira.LoreIn(tools, "org", "create", "--org", flOrg, "--store", store.SSHURL(), "--own", flOwn,
			"--key", "human:mira@agency.example", "--name", "Mira", "--yes").OK(t)
		mira.LoreIn(tools, "store", "folder", "add", "agencystore", flFolder).OK(t)
	})

	w.Step("day1/one-link-installs-the-guard", func() {
		mira.LoreIn(app, "org", "link", "--org", flOrg, "--project", flFolder, "--yes").OK(t).
			Says(t, "as a CLIENT repo of "+flOrg, "leak guard installed", filepath.Join(".git", "hooks", "commit-msg"), filepath.Join(".git", "hooks", "pre-push"))
		for _, h := range []string{"commit-msg", "pre-push"} {
			if b, err := os.ReadFile(filepath.Join(app.Dir, ".git", "hooks", h)); err != nil || !strings.Contains(string(b), "lore") {
				t.Fatalf("the first link left no lore %s hook in .git/hooks: %v", h, err)
			}
		}
		mira.LoreIn(app, "whoami").OK(t).Says(t, "client repo", "guards on").Never(t, "guards OFF")
		if st := app.Git("status", "--porcelain", "--untracked-files=all"); st != "" {
			t.Fatalf("the link changed the client repo's work tree:\n%s", st)
		}
	})

	w.Step("day1/a-commit-repeating-a-rule-is-refused", func() {
		mira.LoreIn(app, "rule", "add", "--body", flRule, "--severity", "must").OK(t)
		jWrite(t, filepath.Join(app.Dir, "CLAUDE.md"), "# Payroll\n\n"+flRule+"\n")
		app.Git("add", "CLAUDE.md")
		r := app.TryGit("commit", "-q", "-m", "Add CLAUDE.md")
		if r.Code == 0 || !strings.Contains(r.String(), "E_LEAK_BLOCKED") || !strings.Contains(r.String(), "CLAUDE.md:3") {
			t.Fatalf("a commit repeating an internal rule was not refused after one link: %s", r)
		}
	})
}
