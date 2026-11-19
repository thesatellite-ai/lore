//go:build stories && !windows

package stories

// WX_ownrepo_test.go — devco D-525 (found by worker wx522 in the write-op
// matrix): an admin starts the org from inside the org's own repo and
// gives the guided setup no own: patterns. Nothing marks the repo as the
// org's own, so lore treats it as a client repo (fail closed, the
// documented rule) and every command agrees; what was wrong is that lore
// fix and whoami said "a client repo" with no cause and no way out.
//
//	day1: StartTeam in platform (no own:), lore init, sync
//	D-525: whoami and lore fix say "treated as a client repo", why (no own:
//	       pattern matches the remote) and the command that declares it own;
//	       after that command whoami says own repo and fix sets the hooks up
//	D-512: the client repo of the org (repos: pattern) keeps its plain lines

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

func TestStoryWXOwnRepoNeverListed(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WX"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.LoreIn(pc, "init").OK(t)
	pc.Git("add", "-A")
	pc.Git("commit", "-q", "--allow-empty", "-m", "lore setup")
	m.LoreIn(pc, "sync").OK(t)

	declare := "lore org config set --org " + wOrg + " --key own --add " + wAppSlug + " --yes"
	s.Step("D-525/says-why-and-the-way-out", func() {
		m.Lore(pc.Dir, "whoami").OK(t).Says(t, "treated as a client repo", "no own: pattern of "+wOrg+" matches "+wAppSlug, declare)
		m.Lore(pc.Dir, "fix").Says(t, "agent hooks", "skipped: treated as a client repo", declare)
	})

	s.Step("D-525/declared-own", func() {
		m.Lore(pc.Dir, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", wAppSlug, "--yes").OK(t)
		r := m.Lore(pc.Dir, "whoami").OK(t).Says(t, "own repo  (no client guards)")
		r.Never(t, "client repo")
		m.Lore(pc.Dir, "fix").Never(t, "client repo")
	})
}
