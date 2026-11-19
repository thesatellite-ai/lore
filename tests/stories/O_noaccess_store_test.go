//go:build stories && !windows

package stories

// O_noaccess_store_test.go — story O, a member without access to a second
// everyone-store (devco D-726). On the owner-shaped laptops the org keeps
// a second store for everyone, but the forge gives the member's login no
// access to it. The member's lore skips it with one quiet note: whoami
// keeps the org row and names it once, reads, context and sync work, and
// a write into it refuses with the true cause (no access; ask an admin of
// the org), never "go online". Every name, email and url is made up.

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

func TestStoryONoAccessStore(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	vault := s.Remote("github.com/" + oOwner + "/lore-vault")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "store", "folder", "add", "team", oFolder).OK(t)
	ada.LoreIn(pc, "org", "store", "add", "--org", oOrg, "--url", vault.SSHURL(), "--push", "--yes").OK(t)
	ada.LoreIn(pc, "store", "folder", "add", vault.SSHURL(), "vendors").OK(t)
	ada.LoreIn(pc, "memory", "add", "--body", "VAULTNOTE the vendor contract renews in march", "--to", "lore-vault/vendors", "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)

	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	// the forge gives Mo's login no access to lore-vault: his git finds no
	// repository there (the answer GitHub gives a login without access)
	nowhere := filepath.Join(filepath.Dir(mo.Home), "no-access", "lore-vault.git")
	for _, from := range []string{"git@" + strings.Replace(vault.Slug, "/", ":", 1) + ".git", "https://" + vault.Slug + ".git"} {
		if out, err := exec.Command("git", "config", "--file", filepath.Join(mo.Home, ".gitconfig"), "--add", "url."+nowhere+".insteadOf", from).CombinedOutput(); err != nil {
			t.Fatalf("deny lore-vault: %v %s", err, out)
		}
	}
	_, line := journeys.Invite(t, ada.Machine, pc.Dir, oMemOrg.Email)
	mwc := mo.CloneTo(web, mo.OrgDir("web"))
	oSetupFlow(t, mo, mwc.Dir, oMSetup, "member", oMemOrg, oAlias, map[string]any{"invite": line}, []string{"~/code/" + oOrg + "/**"})

	s.Step("D-726/member-without-access-to-a-second-everyone-store", func() {
		sy := mo.LoreIn(mwc, "sync")
		oCheck(t, "D-726/member-noaccess-sync", sy.Code == 0 && !strings.Contains(sy.Out(), "ERROR") && !strings.Contains(sy.Out(), "cannot read") &&
			strings.Count(sy.Out(), "lore-vault") <= 1, "sync fails on (or repeats) the store he cannot reach:\n%s", sy)
		who := mo.LoreIn(mwc, "whoami")
		oCheck(t, "D-726/member-noaccess-whoami", who.Code == 0 && strings.Contains(who.Out(), "lore-vault: no access, skipped") && strings.Contains(who.Out(), "Ask an admin of "+oOrg) &&
			strings.Count(who.Out(), "lore-vault: no access") == 1 && !strings.Contains(who.Out(), "cannot read"), "whoami does not name the skipped store once:\n%s", who)
		ctx := mo.Lore(filepath.Dir(mwc.Dir), "context", "--topics")
		oCheck(t, "D-726/member-noaccess-context", ctx.Code == 0 && !strings.Contains(ctx.Out(), "VAULTNOTE"), "context fails or shows the store he cannot read:\n%s", ctx)
		rd := mo.Lore(filepath.Dir(mwc.Dir), "search", "vendor contract")
		oCheck(t, "D-726/member-noaccess-reads", rd.Code == 0 && !strings.Contains(rd.Out(), "VAULTNOTE"), "a read fails or shows the store he cannot read:\n%s", rd)
		wr := mo.Lore(filepath.Dir(mwc.Dir), "memory", "add", "--body", "x", "--to", "lore-vault/vendors", "--yes")
		oCheck(t, "D-726/member-noaccess-write-cause", wr.Code != 0 && strings.Contains(wr.Out(), "no access to lore-vault") && strings.Contains(wr.Out(), "Ask an admin of "+oOrg) &&
			!strings.Contains(wr.Out(), "go online"), "a write into the store he cannot reach does not name the true cause:\n%s", wr)
	})
}
