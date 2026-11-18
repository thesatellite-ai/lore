//go:build stories && !windows

package stories

// O_identity_viainclude_test.go — devco D-594 on the owner-shaped machine
// (ownershape.go, story O): the includeIf gitdir: rule that gives the org
// folder the org identity lives in a file ~/.gitconfig pulls in with
// [include] path=. git follows the include when it commits there, but
// setup check --step identity read only the top file and warned "no
// includeIf gitdir: rule for that folder sets <org email>".

import (
	"strings"
	"testing"
)

func TestStoryOIdentityViaInclude(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}, ViaInclude: true})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	s.Step("D-594/fixture-identity-via-include", func() {
		if got := ada.GitUser(t, pc.Dir); got != oAdminOrg.Email {
			t.Fatalf("fixture: the org repo's git user is %q, want %q (includeIf in an [include]d file)", got, oAdminOrg.Email)
		}
	})
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/*"})

	s.Step("D-594/identity-check-follows-include", func() {
		r := ada.Lore(ada.Home, "setup", "check", "--setup", oSetup, "--step", "identity")
		r.Never(t, "no includeIf", "--global")
		// devco:D-595: with no --answers, a finished setup's check is of its
		// saved values (it said "Your name is required"), and its result is
		// the health setup list shows
		if !strings.Contains(r.Stdout, "git uses "+oAdminOrg.Email) {
			t.Errorf("setup check --step identity does not check the saved identity:\n%s", r)
		}
		for _, args := range [][]string{{"setup", "list"}, {"doctor", "--all"}} {
			ada.Lore(ada.Home, args...).Never(t, "--global", "identity.git_email")
		}
	})
}
