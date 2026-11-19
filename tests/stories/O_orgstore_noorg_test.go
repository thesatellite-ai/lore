//go:build stories && !windows

package stories

// O_orgstore_noorg_test.go — the ownershape cell "an admin writes into the
// org store from a repo no org matches" (devco D-694, story O). On the
// owner-shaped laptop a repo outside ~/code/<org>/ whose remote no org
// owns gets the personal git identity, so the open there is not the org
// identity; a write `--to <org>/<folder>` lands in the org's store and is
// committed as the org identity. The role check judges that same
// identity: the admin's write is allowed and authored by their org
// identity, while a member's write from such a repo is judged as the
// member they are in the org (refused in a folder of a team they are not
// on, with the ask-an-admin line; allowed in their own team's folder).
// Every name, email and url is made up.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// oNoOrgMemberKey is the member half of the cell: on the owner-shaped
// machine a member's out-of-team write from a repo no org matches is
// judged by the org store's roster (devco D-694: the store-only open of a
// folder with no lore project had no write guard).
const oNoOrgMemberKey = "D-694/member-from-no-org-repo-refused"

func TestStoryOOrgStoreWriteFromNoOrgRepo(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })
	side := s.Seed(s.Remote("github.com/elsewhere/side"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "store", "folder", "add", "team", oFolder).OK(t)
	ada.LoreIn(pc, "store", "folder", "add", "team", "api").OK(t)
	ada.LoreIn(pc, "team", "add", "--handle", "@api", "--members", oAdminOrg.Email, "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)

	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	_, line := journeys.Invite(t, ada.Machine, pc.Dir, oMemOrg.Email)
	mwc := mo.CloneTo(web, mo.OrgDir("web"))
	oSetupFlow(t, mo, mwc.Dir, oMSetup, "member", oMemOrg, oAlias, map[string]any{"invite": line}, []string{"~/code/" + oOrg + "/**"})
	mo.LoreIn(mwc, "sync").OK(t)
	ada.LoreIn(pc, "team", "add", "--handle", "@"+oFolder, "--members", oMemOrg.Email, "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)
	mo.LoreIn(mwc, "sync").OK(t)

	s.Step("D-694/admin-writes-into-the-org-store-from-a-repo-no-org-matches", func() {
		sc := ada.CloneTo(side, ada.Code("side"))
		if got := ada.GitUser(t, sc.Dir); got != oAdminPersonal.Email {
			t.Fatalf("fixture: the side repo's git user is %q, want the personal identity %q", got, oAdminPersonal.Email)
		}
		clone := oStoreClone(ada, oOwner, "lore-team")
		r := ada.LoreIn(sc, "memory", "add", "--body", "ADMINSIDE invoices go out on the 1st", "--to", oOrg+"/api", "--yes")
		oCheck(t, "D-694/admin-from-no-org-repo", r.Code == 0, "the org's admin writing --to %s/api from a repo no org matches was refused:\n%s", oOrg, r)
		if r.Code == 0 {
			author := strings.TrimSpace(s.Git(clone, "log", "-1", "--format=%ae"))
			oCheck(t, "D-694/admin-from-no-org-repo-author", author == oAdminOrg.Email, "the admin's write into the org store is authored %q, want the org identity %q", author, oAdminOrg.Email)
			row := wthRow(t, clone, "memories", "body", "ADMINSIDE invoices go out on the 1st")
			by, _ := wthJSON(t, filepath.Join(clone, row))["created_by"].(string)
			actor := wthRow(t, clone, "actors", "stable_key", "human:"+oAdminOrg.Email)
			oCheck(t, "D-694/admin-from-no-org-repo-stamp", strings.TrimSuffix(filepath.Base(actor), ".json") == by, "the admin's row is stamped %s, want the org identity's actor %s", by, actor)
		}
	})

	s.Step("D-694/member-from-a-repo-no-org-matches-keeps-the-role-rule", func() {
		sc := mo.CloneTo(side, mo.Code("side"))
		r := mo.LoreIn(sc, "memory", "add", "--body", "member side note for api", "--to", oOrg+"/api", "--yes")
		refused := r.Code != 0
		for _, want := range []string{"E_REFUSED", "only an admin of the org", "folder of a team they are on", "on the roster of " + oOrg, "ask an admin of " + oOrg + " (" + oAdminOrg.Name + ")", "nothing was written"} {
			refused = refused && strings.Contains(r.Out(), want)
		}
		oCheck(t, oNoOrgMemberKey, refused, "a member's write --to %s/api (a team he is not on) from a repo no org matches is not refused with the admin line:\n%s", oOrg, r)
		// judged as the member he is in the org: his own team's folder is his
		mo.LoreIn(sc, "memory", "add", "--body", "member side note for web", "--to", oOrg+"/"+oFolder, "--yes").OK(t)
		if _, err := os.Stat(oStoreClone(mo, oOwner, "lore-team")); err == nil {
			author := strings.TrimSpace(s.Git(oStoreClone(mo, oOwner, "lore-team"), "log", "-1", "--format=%ae"))
			oCheck(t, "D-694/member-from-no-org-repo-author", author == oMemOrg.Email, "the member's write into the org store is authored %q, want the org identity %q", author, oMemOrg.Email)
		}
	})
}
