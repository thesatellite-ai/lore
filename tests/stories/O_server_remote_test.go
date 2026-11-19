//go:build stories && !windows

package stories

// O_server_remote_test.go — devco D-706 on the owner-shaped machine (story
// O): the owner keeps some of the org's repos on their own server and
// clones them scp-style (<server>:/srv/projects/<org>/<repo>, no forge
// host). In the org setup such a repo was a client repo by the fail-closed
// default with no cause and no way out. Two cells: a server name the ssh
// config does not rename (printed as typed, with the "this machine's name"
// caveat) and one it renames (Host devbox / HostName devbox.internal.example:
// the printed pattern is the resolved name, and works as printed).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOServerRemoteMarkedOwn(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	rollout := s.Seed(s.Remote("github.com/"+oOwner+"/rollout"), func(dir string) { PlainRepo(t, dir) })
	ops := s.Seed(s.Remote("github.com/"+oOwner+"/ops"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	// the server the ssh config renames
	sshCfg := filepath.Join(ada.Home, ".ssh", "config")
	f, err := os.OpenFile(sshCfg, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nHost devbox\n  HostName devbox.internal.example\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	cells := []struct {
		name, origin, remote, caveat string
		repo                         string
	}{
		{"plain-server-name", "buildbox:/srv/projects/" + oOrg + "/rollout.git", "buildbox/srv/projects/" + oOrg + "/rollout", "buildbox is this machine's name for the server", "rollout"},
		{"ssh-config-renamed", "ssh://devbox/srv/projects/" + oOrg + "/ops.git", "devbox.internal.example/srv/projects/" + oOrg + "/ops", "", "ops"},
	}
	for _, c := range cells {
		src := rollout
		if c.repo == "ops" {
			src = ops
		}
		cc := ada.CloneTo(src, ada.OrgDir(c.repo))
		cc.Git("remote", "set-url", "origin", c.origin)
		declare := "lore org config set --org " + oOrg + " --key own --add " + c.remote + " --yes"
		s.Step("D-706/"+c.name+"/says-why", func() {
			r := ada.Lore(cc.Dir, "whoami").OK(t)
			r.Says(t, "treated as a client repo", "no own: pattern of "+oOrg+" matches this repo's remote "+c.remote, declare)
			if c.caveat != "" {
				r.Says(t, c.caveat)
			} else {
				r.Never(t, "is this machine's name for the server")
			}
		})
		s.Step("D-706/"+c.name+"/own-as-printed", func() {
			ada.Lore(cc.Dir, strings.Fields(strings.TrimPrefix(declare, "lore "))...).OK(t)
			ada.Lore(cc.Dir, "whoami").OK(t).Says(t, "own repo  (no client guards)").Never(t, "client repo")
		})
	}
}
