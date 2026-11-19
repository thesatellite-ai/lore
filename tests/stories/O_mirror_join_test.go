//go:build stories && !windows

package stories

// O_mirror_join_test.go — devco D-680 (D-626 a third time) on the
// owner-shaped machine (ownershape.go, story O), laid out as the owner's
// machine was when it still failed: the member's ~/.gitconfig sends the
// store's own url, in the github.com AND the ssh alias spelling, to a
// local mirror (url.<mirror>.insteadOf), so git reaches the store without
// ssh, while what ssh would do with the alias is only in ~/.ssh/config, or
// nowhere lore reads (a HOME whose ssh config does not name the alias).
// `setup apply --step join` with git@<alias>:<owner>/<store>.git failed
// E_SETUP_ORG_MISMATCH "an org entry named <org> exists for store
// git@github.com:…" although no entry existed after the refusal, and the
// printed `org rename` hint failed; the https spelling joined but saved
// the github.com spelling instead of the one the person gave.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// oMirrorRules points every spelling of store (git@github.com:, the alias,
// https://github.com/, ssh://git@github.com/) at mirror, the way a person
// with a local mirror of the store, or a corporate git proxy, configures
// git; pushes to any other github.com url go nowhere (pushInsteadOf).
func oMirrorRules(t testing.TB, m *OwnerMachine, mirror, owner, repo string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "[url %q]\n", mirror)
	for _, u := range []string{"git@github.com:", "git@" + m.Shape.Alias + ":", "https://github.com/", "ssh://git@github.com/"} {
		fmt.Fprintf(&b, "\tinsteadOf = %s%s/%s.git\n", u, owner, repo)
	}
	appendFile(t, filepath.Join(m.Home, ".gitconfig"), b.String())
}

// oForgetAlias drops the alias's Host block from ~/.ssh/config: the
// alias then resolves nowhere lore reads (git still reaches the store
// through the mirror rule).
func oForgetAlias(t testing.TB, m *OwnerMachine) {
	t.Helper()
	p := filepath.Join(m.Home, ".ssh", "config")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	skip := false
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) > 0 && strings.EqualFold(f[0], "host") {
			skip = len(f) > 1 && f[1] == m.Shape.Alias
		}
		if !skip {
			keep = append(keep, l)
		}
	}
	writeFile(t, p, strings.Join(keep, "\n"))
}

func TestStoryOMirrorMemberJoin(t *testing.T) {
	cells := []struct {
		name, spell string
		sshAlias    bool
		// saved is what config.yaml must hold so this machine reaches the
		// store the way the person did: the spelling given, or for an
		// alias the ssh config resolves the canonical url plus the access
		// rule that routes it through the alias (brief 3.7)
		saved string
	}{
		{"alias-ssh-config", "git@" + oAlias + ":" + oOwner + "/lore-team.git", true, "via: git@" + oAlias + ":" + oOwner + "/"},
		{"alias-no-ssh-config", "git@" + oAlias + ":" + oOwner + "/lore-team.git", false, "url: git@" + oAlias + ":" + oOwner + "/lore-team.git"},
		{"https", "https://github.com/" + oOwner + "/lore-team.git", true, "url: https://github.com/" + oOwner + "/lore-team.git"},
		{"ssh-scheme", "ssh://git@github.com/" + oOwner + "/lore-team.git", true, "url: ssh://git@github.com/" + oOwner + "/lore-team.git"},
		{"canonical", "git@github.com:" + oOwner + "/lore-team.git", true, "url: git@github.com:" + oOwner + "/lore-team.git"},
	}
	for _, c := range cells {
		t.Run(c.name, func(t *testing.T) {
			s := New(t, "O")
			team := s.Remote("github.com/" + oOwner + "/lore-team")
			platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
			web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })
			ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
			pc := ada.CloneTo(platform, ada.OrgDir("platform"))
			oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
				map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
			ada.LoreIn(pc, "member", "add", "--email", oMemOrg.Email, "--name", oMemOrg.Name, "--yes").OK(t)
			ada.LoreIn(pc, "sync").OK(t)

			mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
			mwc := mo.CloneTo(web, mo.OrgDir("web"))
			oMirrorRules(t, mo, team.Dir, oOwner, "lore-team")
			if !c.sshAlias {
				oForgetAlias(t, mo)
			}
			s.Step("D-680/"+c.name+"/member-join-in-one-run", func() {
				plan := mo.Lore(mwc.Dir, "setup", "join", "--invite", c.spell, "--no-open").OK(t)
				plan.Says(t, "--setup "+oOrg+" ")
				// the github step skipped, as on the owner's machine (no gh
				// login for the account): the join alone decides
				oApply(t, mo, mwc.Dir, oOrg, "kind", map[string]any{"kind": "member", "name": oOrg})
				oApply(t, mo, mwc.Dir, oOrg, "identity", map[string]any{"name": oMemOrg.Name, "email": oMemOrg.Email})
				oApply(t, mo, mwc.Dir, oOrg, "github", map[string]any{})
				oApply(t, mo, mwc.Dir, oOrg, "personal_store", map[string]any{"personal_store": "local"})
				r := oJoin(t, mo, mwc.Dir, oOrg, c.spell)
				r.Never(t, "E_SETUP_ORG_MISMATCH", "made no org entry", "--as ").OK(t)
				mo.Lore(mo.Home, "setup", "list").OK(t).Says(t, oOrg+"  member").Never(t, "not saved yet: "+oOrg+" ")
				cfg, err := os.ReadFile(filepath.Join(mo.LorePath(), "config.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(cfg), c.saved) {
					t.Errorf("the org entry does not reach the store the way the person gave it (want %q):\n%s", c.saved, cfg)
				}
				oJoin(t, mo, mwc.Dir, oOrg, c.spell).Never(t, "--as ", "already exists", "E_SETUP_ORG_MISMATCH").OK(t)
				mo.Lore(mwc.Dir, "sync").OK(t)
			})
		})
	}
}
