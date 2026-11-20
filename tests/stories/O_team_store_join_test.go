//go:build stories && !windows

package stories

// O_team_store_join_test.go — the ownershape cells of a member who is on a
// team when they join (devco D-734, story O). An owner-shaped admin keeps
// lore-secret for team @secret and links the web repo to its folder; Mo,
// a member (not an admin), is invited onto @secret and joins: his join and
// sync clone and attach the team store, and in his web clone whoami reads
// the link. Then lore-org.yaml gains an audience kind this lore does not
// know (what a newer admin lore could write): Mo's lore keeps the store
// off his machine and whoami and doctor say which store and to upgrade
// (lore 0.1.18 read team:@secret that way and said nothing, so a team
// member never got the team's store). Every name, email and url is made up.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

func TestStoryOTeamStoreAtJoin(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	secret := s.Remote("github.com/" + oOwner + "/lore-secret")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "team", "add", "--handle", "@secret", "--members", oAdminOrg.Email, "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)
	ada.LoreIn(pc, "org", "store", "add", "--org", oOrg, "--url", secret.SSHURL(), "--audience", "team:@secret", "--push", "--yes").OK(t)
	ada.LoreIn(pc, "store", "folder", "add", secret.SSHURL(), "secret").OK(t)
	awc := ada.CloneTo(web, ada.OrgDir("web"))
	ada.LoreIn(awc, "org", "link", "--org", oOrg, "--store", secret.SSHURL(), "--project", "secret", "--yes").OK(t)
	ada.LoreIn(awc, "memory", "add", "--body", "SECRETNOTE the client deploys from the blue cluster", "--yes").OK(t)
	ada.LoreIn(awc, "sync").OK(t)

	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	_, line := journeys.Invite(t, ada.Machine, pc.Dir, oMemOrg.Email, "--team", "@secret")
	mwc := mo.CloneTo(web, mo.OrgDir("web"))
	oSetupFlow(t, mo, mwc.Dir, oMSetup, "member", oMemOrg, oAlias, map[string]any{"invite": line}, []string{"~/code/" + oOrg + "/**"})

	s.Step("D-734/member-on-the-team-at-join-gets-the-store", func() {
		mo.LoreIn(mwc, "sync").OK(t)
		cfg, _ := os.ReadFile(mo.LorePath("config.yaml"))
		oCheck(t, "D-734/member-in-team-at-join-has-store", strings.Contains(string(cfg), "lore-secret"), "a member on @secret at join has no team store in his org entry after join and sync:\n%s", cfg)
		r := mo.LoreIn(mwc, "whoami")
		oCheck(t, "D-734/member-in-team-at-join-link-resolves", r.Code == 0 && strings.Contains(r.Out(), "lore-secret/secret (this repo's link)") && strings.Contains(r.Out(), "1 link(s) for this repo"),
			"the web repo's link to the team store does not resolve for a member on @secret:\n%s", r)
		r = mo.Lore(filepath.Dir(mwc.Dir), "search", "blue cluster")
		oCheck(t, "D-734/member-in-team-at-join-reads", r.Code == 0 && strings.Contains(r.Out(), "SECRETNOTE"), "a member on @secret does not read the team store:\n%s", r)
	})

	s.Step("D-734/an-audience-this-lore-cannot-read-is-said", func() {
		ed := ada.CloneTo(team, ada.OrgDir("org-edit"))
		f := "org/lore-org.yaml"
		raw, ok := ed.ReadFile(f)
		if !ok {
			f = oOrg + "/lore-org.yaml"
			raw, ok = ed.ReadFile(f)
		}
		if !ok || !strings.Contains(raw, "audience: team:@secret") {
			t.Fatalf("%s (%v):\n%s", f, ok, raw)
		}
		ed.WriteFile(f, strings.Replace(raw, "audience: team:@secret", "audience: group:@secret", 1))
		ed.CommitAll("a newer lore's audience kind")
		ed.Git("push", "-q", "origin", "HEAD:main")
		mo.LoreIn(mwc, "sync").OK(t)
		who := mo.LoreIn(mwc, "whoami")
		doc := mo.LoreIn(mwc, "doctor")
		said := func(out string) bool {
			return strings.Contains(out, "store lore-secret is for group:@secret") && strings.Contains(out, "cannot read that audience") && strings.Contains(out, "lore upgrade")
		}
		oCheck(t, "D-734/unknown-audience-whoami", said(who.Out()), "whoami does not say the store of an unknown audience is left out:\n%s", who)
		oCheck(t, "D-734/unknown-audience-doctor", said(doc.Out()), "doctor does not say the store of an unknown audience is left out:\n%s", doc)
	})
}
