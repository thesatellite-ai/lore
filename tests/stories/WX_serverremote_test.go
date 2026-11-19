//go:build stories && !windows

package stories

// WX_serverremote_test.go — devco D-706: inside an org setup, a repo whose
// only remote is a server reached by its ssh name (scp-style
// buildbox:/srv/git/acme/tools.git, no forge host) is a client repo by the
// fail-closed default, and the commit hook writes lore.clientrepo=true.
// The rule stays; what was wrong is that whoami, lore fix and the hook said
// nothing about why and named no way out (the D-525 line skipped a host
// with no dot). Now each names the remote, the cause and the own: command
// for exactly this remote, the command works as printed, and after it the
// repo is own and the hook removes the mark on the next commit.
//
//	day1: StartTeam (no own:), a repo in the setup's folders with origin
//	      buildbox:/srv/git/acme/tools.git, lore init --yes, commit
//	D-706: whoami + the hook's mark line say why and the command
//	       → run it as printed → whoami: own repo; next commit unsets the mark

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// wxServerOrigin is the scp-style origin of the repo on a made-up server
// and wxServerRemote its normalised form (the own: pattern lore prints).
const (
	wxServerOrigin = "buildbox:/srv/git/acme/tools.git"
	wxServerRemote = "buildbox/srv/git/acme/tools"
)

func TestStoryWXServerRemote(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WX"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	tools := s.Seed(s.Remote("github.com/acme-co/tools"), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})

	tc := m.Clone(tools, "tools")
	m.MoveClone(t, tc, filepath.Join(m.Home, "code", "tools"))
	tc.Git("remote", "set-url", "origin", wxServerOrigin)
	declare := "lore org config set --org " + wOrg + " --key own --add " + wxServerRemote + " --yes"
	cause := "no own: pattern of " + wOrg + " matches this repo's remote " + wxServerRemote + ", so lore treats it as a client repo"

	s.Step("D-706/server-remote-says-why-and-the-way-out", func() {
		m.Lore(tc.Dir, "whoami").OK(t).Says(t, "treated as a client repo", cause, declare, "buildbox is this machine's name for the server")
		m.LoreIn(tc, "init", "--yes").OK(t)
		tc.Git("add", "-A")
		r := tc.TryGit("commit", "-q", "-m", "lore setup")
		if r.Code != 0 {
			t.Fatalf("commit in the server-remote repo: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
		r.Says(t, "set lore.clientrepo=true", cause, declare)
		if got := strings.TrimSpace(tc.Git("config", "--local", "--get", "lore.clientrepo")); got != "true" {
			t.Fatalf("lore.clientrepo = %q, want true", got)
		}
	})

	s.Step("D-706/declared-own-as-printed", func() {
		args := strings.Fields(strings.TrimPrefix(declare, "lore "))
		m.Lore(tc.Dir, args...).OK(t)
		m.Lore(tc.Dir, "whoami").OK(t).Says(t, "own repo  (no client guards)").Never(t, "client repo")
		r := tc.TryGit("commit", "-q", "--allow-empty", "-m", "after own")
		if r.Code != 0 {
			t.Fatalf("commit after own: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
		r.Says(t, "removed lore.clientrepo")
		if got := tc.TryGit("config", "--local", "--get", "lore.clientrepo"); got.Code == 0 {
			t.Fatalf("lore.clientrepo still set: %q", got.Stdout)
		}
	})
}
