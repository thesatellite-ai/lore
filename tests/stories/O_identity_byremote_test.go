//go:build stories && !windows

package stories

// O_identity_byremote_test.go — devco D-533 on the owner-shaped machine
// (ownershape.go, story O): the org identity comes from includeIf blocks
// keyed on the remote (hasconfig:remote.*.url:git@<host>:<owner>/**, the
// github.com and the alias spelling) with a relative include path, and the
// org's setup lists no folders. setup list and doctor --all from $HOME
// warned identity.git_email there and advised `git config --global
// user.email <org email>` (devco D-530), which would overwrite the personal
// identity every other repo uses; the gitdir: shape of TestStoryOOwnerShape
// was the one shape the old check already understood.

import (
	"testing"
)

func TestStoryOIdentityByRemote(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}, IncludeByRemote: true})
	// outside any org folder: only the remote gives this repo the org identity
	pc := ada.CloneTo(platform, ada.Code("work", "platform"))
	s.Git(pc.Dir, "remote", "set-url", "origin", "git@"+oAlias+":"+oOwner+"/platform.git") // the owner's clones keep the alias spelling
	s.Step("D-533/fixture-identity-by-remote", func() {
		if got := ada.GitUser(t, pc.Dir); got != oAdminOrg.Email {
			t.Fatalf("fixture: the org repo's git user is %q, want %q (includeIf hasconfig:remote.*.url); origin %s", got, oAdminOrg.Email, s.Git(pc.Dir, "config", "--get-regexp", "^remote"))
		}
		if got := ada.GitUser(t, ada.Home); got != oAdminPersonal.Email {
			t.Fatalf("fixture: the global git user is %q, want the personal %q", got, oAdminPersonal.Email)
		}
	})
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{})

	s.Step("D-533/identity-advice", func() {
		for _, args := range [][]string{{"setup", "list"}, {"doctor", "--all"}} {
			r := ada.Lore(ada.Home, args...)
			r.Never(t, "--global", "identity.git_email")
		}
	})
}
