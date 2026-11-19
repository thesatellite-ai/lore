//go:build stories && !windows

package stories

// O_alias_whoami_test.go — devco D-531 on the owner-shaped machine
// (ownershape.go, story O): a client repo cloned over the org account's ssh
// alias (origin git@<alias>:<client owner>/<repo>.git, the alias named by
// ~/.ssh/config Host/HostName github.com, the insteadOf rules and the
// setup's host_alias). whoami showed the remote as <alias>/<owner>/<repo>,
// matched no org (the org's repos: github.com/<owner>/* never matched), fell
// back to the personal identity, and with --setup offered to write the alias
// pattern into the team's shared own: list. Now the alias is resolved
// before anything is matched or printed. devco D-532: `org link --remote`
// typed in the alias spelling links the same repo. devco D-576: the repos
// rows record the resolved spelling too.

import (
	"testing"
)

// oClientOwner is the GitHub owner of story O's alias-cloned client repo.
const oClientOwner = "clientco"

func TestStoryOAliasWhoami(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	portal := s.Seed(s.Remote("github.com/"+oClientOwner+"/portal"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner, oClientOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "org", "config", "set", "--org", oOrg, "--key", "repos", "--value", "github.com/"+oClientOwner+"/*", "--yes").OK(t)

	// outside the setup's folders (as in the owner's case): only the remote
	// can tie this repo to the org
	cc := ada.CloneTo(portal, ada.Code("clients", "portal"))
	// the owner's clone keeps the alias spelling in its origin
	aliasURL := "git@" + oAlias + ":" + oClientOwner + "/portal.git"
	s.Git(cc.Dir, "remote", "set-url", "origin", aliasURL)

	want := "github.com/" + oClientOwner + "/portal"
	aliasForm := oAlias + "/" + oClientOwner
	s.Step("D-531/whoami-resolves-the-alias", func() {
		r := ada.Lore(cc.Dir, "whoami").OK(t)
		r.Says(t, "remotes    "+want, "matched by: repos pattern github.com/"+oClientOwner+"/* of "+oOrg, "client repo", oAdminOrg.Email)
		r.Never(t, aliasForm, "nothing matched", "fallback identity", oAdminPersonal.Email)
	})
	s.Step("D-531/no-alias-pattern-in-any-hint", func() {
		r := ada.Lore(cc.Dir, "whoami", "--setup", oSetup)
		r.Never(t, aliasForm, "--key own --add "+oAlias)
		r = ada.Lore(cc.Dir, "whoami", "--json").OK(t)
		r.Says(t, want)
		r.Never(t, aliasForm)
	})
	// devco:D-532: `org link --remote` typed in the alias spelling (what the
	// owner's origin says) names the same repo as the resolved remote; it
	// was refused as "not a remote of this repo"
	s.Step("D-532/link-by-typed-alias-remote", func() {
		r := ada.Lore(cc.Dir, "org", "link", "--org", oOrg, "--project", "portal", "--remote", aliasURL, "--yes").OK(t)
		r.Says(t, "linked")
		r.Never(t, "not a remote of this repo", aliasForm)
		ada.Lore(cc.Dir, "whoami").OK(t).Says(t, "portal").Never(t, aliasForm)
	})
	// devco:D-576: the repos row of an alias clone (init's identity, `repo
	// add --origin` typed in the alias spelling) records the github.com
	// spelling every other path keys the repo by, so a teammate's clone
	// from github.com is the same address, never an unconfirmed one
	s.Step("D-576/repo-rows-key-the-resolved-remote", func() {
		resolved := "git@github.com:" + oClientOwner + "/portal.git"
		ada.Lore(cc.Dir, "init", "--yes").OK(t)
		ada.Lore(cc.Dir, "repo", "list", "--json").OK(t).Says(t, `"origin_url": "`+resolved+`"`).Never(t, oAlias)
		ada.Lore(cc.Dir, "repo", "add", "portal-two", "--origin", aliasURL).OK(t)
		ada.Lore(cc.Dir, "repo", "list").OK(t).Says(t, "portal-two  "+resolved).Never(t, oAlias)
		ada.Lore(cc.Dir, "sync").OK(t)
		ada.Lore(cc.Dir, "doctor").Says(t, "same address "+want).Never(t, "is unconfirmed", aliasForm)
	})
}
