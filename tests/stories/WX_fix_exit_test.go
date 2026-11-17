//go:build stories && !windows

package stories

// WX_fix_exit_test.go — devco D-522, D-524 (found by the write-op matrix):
// `lore fix` exits as it speaks, and in a store clone it says what applies.
//
//	day1: an admin starts the org on platform, sets up a repo that
//	      gitignores .lore/, sends everything
//	D-522: lore fix in either ends "Everything is current." with warning lines
//	       only: exit 0 (it exited 1 with no error line)
//	D-524: lore fix in the store clone (its root and a folder) says it is a
//	       store clone, not a code repo, and names lore store doctor (it said
//	       "not inside a git repo here", in a git repo)

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// wxErrorLine is a line of fix's own output that explains a non-zero exit:
// an error line or the failed-steps line.
var wxErrorLine = regexp.MustCompile(`(?m)^(error: |ERROR: \[E_|Not everything is current)`)

func TestStoryWXFixExitAndStoreClone(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WX"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	ign := s.Seed(s.Remote(wIgnSlug), func(dir string) {
		PlainRepo(t, dir)
		writeFile(t, filepath.Join(dir, ".gitignore"), ".lore/\n")
	})
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)

	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.LoreIn(pc, "init").OK(t)
	m.LoreIn(pc, "store", "folder", "add", "team", wFolder).OK(t)
	wc := m.Clone(ign, "private-notes")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "private-notes"))
	m.LoreIn(wc, "init").OK(t)
	for _, c := range []*teamharness.Clone{pc, wc} {
		c.Git("add", "-A")
		c.Git("commit", "-q", "--allow-empty", "-m", "lore setup")
	}
	m.LoreIn(pc, "sync").OK(t)

	s.Step("D-522/fix-exits-as-it-speaks", func() {
		for _, dir := range []string{wc.Dir, pc.Dir} {
			m.Lore(dir, "fix") // the first run may repair something
			r := m.Lore(dir, "fix")
			out := r.Out()
			if !strings.Contains(out, "Status: ") {
				t.Fatalf("lore fix in %s printed no status:\n%s", dir, r)
			}
			if r.Code != 0 && !wxErrorLine.MatchString(out) {
				t.Errorf("lore fix in %s exited %d with no line that says why:\n%s", dir, r.Code, r)
			}
			if r.Code == 0 && wxErrorLine.MatchString(out) {
				t.Errorf("lore fix in %s exited 0 after an error line:\n%s", dir, r)
			}
		}
	})

	s.Step("D-524/fix-in-a-store-clone", func() {
		root := m.LorePath("stores", "acme-co", "lore-team")
		for _, dir := range []string{root, wFindDir(t, root, wFolder)} {
			r := m.Lore(dir, "fix")
			// the repo-only class (devco D-523, owner D-508/D-509): exit 23
			r.Fails(t, 23, "is a lore store clone, not a code repo", "lore store doctor", "cd into", "--store does not apply")
			r.Never(t, "not inside a git repo")
		}
	})
}
