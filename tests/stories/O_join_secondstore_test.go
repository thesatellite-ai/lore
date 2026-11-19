//go:build stories && !windows

package stories

// O_join_secondstore_test.go — the ownershape cell "a new member joins by
// the url of one of the org's other stores" (devco D-721, story O). On the
// owner-shaped machines the admin runs the org with a main store (it holds
// lore-org.yaml) and two more: one added with `lore org store add` (its
// lore-store.yaml names the org and main store from that commit) and an
// old one, listed while it was still an empty repo and made a lore store
// later, so nothing named the org in it. The admin's next sync names the
// org in the old store, once. A member who joins with either store's url
// (not the main one) gets the org: setup join says it is a store of the
// org and its printed join line answers with the main store url, and the
// join step with the other store's url follows to the main store. Before,
// both said "the store holds no org yet: the admin who made the org has
// not sent it". Every name, email and url is made up.

import (
	"strings"
	"testing"
)

func TestStoryOJoinBySecondStore(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	ops := s.Remote("github.com/" + oOwner + "/lore-ops")
	old := s.Remote("github.com/" + oOwner + "/lore-old")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })
	scratch := s.Seed(s.Remote("github.com/"+oOwner+"/scratch"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	s.Step("D-721/org-store-add-names-the-org-in-the-added-store", func() {
		sc := ada.CloneTo(scratch, ada.OrgDir("scratch"))
		ada.LoreIn(sc, "store", "init", "--name", "ops", "--url", ops.SSHURL(), "--yes").OK(t)
		ada.LoreIn(pc, "org", "store", "add", "--org", oOrg, "--url", ops.SSHURL(), "--push", "--yes").OK(t).
			Says(t, "lore-store.yaml names org "+oOrg, "lore sync sends it")
		// the old shape: listed while an empty repo, then made a lore store
		ada.LoreIn(pc, "org", "store", "add", "--org", oOrg, "--url", old.SSHURL(), "--push", "--yes").OK(t).Says(t, "no lore store yet")
		ada.LoreIn(sc, "store", "init", "--name", "old", "--url", old.SSHURL(), "--yes").OK(t)
		ada.LoreIn(pc, "member", "add", "--email", oMemOrg.Email, "--name", oMemOrg.Name, "--yes").OK(t)
	})

	s.Step("D-721/the-admins-sync-sends-the-marker-and-names-the-org-in-the-old-store-once", func() {
		ada.LoreIn(pc, "sync").OK(t).Says(t, "named org "+oOrg, "lore-old")
		got, _ := ops.Read("refs/heads/main", "lore-store.yaml")
		oCheck(t, "D-721/ops-marker", strings.Contains(got, "org: "+oOrg) && strings.Contains(got, "org_store: "+team.SSHURL()),
			"the added store's lore-store.yaml does not name org %s and main store %s after the sync:\n%s", oOrg, team.SSHURL(), got)
		got, _ = old.Read("refs/heads/main", "lore-store.yaml")
		oCheck(t, "D-721/old-marker", strings.Contains(got, "org: "+oOrg) && strings.Contains(got, "org_store: "+team.SSHURL()),
			"the admin's sync did not name the org in the old store:\n%s", got)
		ada.LoreIn(pc, "sync").OK(t).Never(t, "named org")
	})

	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	mwc := mo.CloneTo(web, mo.OrgDir("web"))

	s.Step("D-721/setup-join-by-another-store-prints-the-main-store", func() {
		for _, r := range []string{ops.SSHURL(), old.SSHURL()} {
			mo.Lore(mwc.Dir, "setup", "join", "--invite", r, "--no-open").OK(t).
				Says(t, "a store of org "+oOrg, `"invite":"`+team.SSHURL()+`"`).
				Never(t, "has not sent it", "cannot tell")
		}
	})

	s.Step("D-721/the-join-step-by-another-store-joins-the-org", func() {
		oSetupFlow(t, mo, mwc.Dir, oMSetup, "member", oMemOrg, oAlias, map[string]any{"invite": ops.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
		mo.Lore(mwc.Dir, "org", "list").OK(t).Says(t, oOrg)
		mo.Lore(mwc.Dir, "store", "list").OK(t).Says(t, "lore-ops", "lore-old")
	})
}
