//go:build stories && !windows

package stories

// O_alias_linkattach_test.go — devco D-684 follow-up on the owner-shaped
// machine (ownershape.go, story O): a client repo linked to the org
// (`org link --org <org> --project <folder>`) that sits outside every
// setup's folders (setup none) attaches the linked folder whatever
// spelling its origin uses to reach github.com: the github.com url, the
// ssh alias ~/.ssh/config names (Host <alias> / HostName github.com), a
// dotted alias only a url.<base>.insteadOf rule routes, or https. On the
// owner's machine the alias-spelled client repo attached no store, so its
// sync took the waiting-clone path.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAliasLinkedClientAttaches(t *testing.T) {
	const client = "clientco"
	const dotted = "github.com-acme"
	cells := []struct {
		name, origin string
		// rules are extra ~/.gitconfig lines this spelling needs to reach github.com
		rules string
		// pattern: the org also lists github.com/<client>/* in repos
		pattern bool
		// forget: ~/.ssh/config does not name the alias (the owner's run
		// used a fake HOME: the alias resolved nowhere lore reads)
		forget bool
	}{
		{"github", "git@github.com:" + client + "/shop.git", "", false, false},
		{"alias-ssh-config", "git@" + oAlias + ":" + client + "/shop.git", "", false, false},
		{"alias-ssh-config-pattern", "git@" + oAlias + ":" + client + "/shop.git", "", true, false},
		{"dotted-alias-insteadof", "git@" + dotted + ":" + client + "/shop.git", fmt.Sprintf("[url \"git@github.com:\"]\n\tinsteadOf = git@%s:\n", dotted), false, false},
		{"https", "https://github.com/" + client + "/shop.git", "", false, false},
		{"alias-unresolved", "git@" + oAlias + ":" + client + "/shop.git", "", false, true},
		{"alias-unresolved-pattern", "git@" + oAlias + ":" + client + "/shop.git", "", true, true},
		{"alias-unresolved-pushguard", "git@" + oAlias + ":" + client + "/shop.git", fmt.Sprintf("[url \"/nonexistent/guard/\"]\n\tpushInsteadOf = git@%s:\n", oAlias), true, true},
	}
	for _, c := range cells {
		for _, place := range []string{"no-setup", "setup"} {
			t.Run(c.name+"/"+place, func(t *testing.T) {
				s := New(t, "O")
				team := s.Remote("github.com/" + oOwner + "/lore-team")
				platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
				shop := s.Seed(s.Remote("github.com/"+client+"/shop"), func(dir string) { PlainRepo(t, dir) })
				ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
				pc := ada.CloneTo(platform, ada.OrgDir("platform"))
				oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
					map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
				if c.pattern {
					ada.LoreIn(pc, "org", "config", "set", "--org", oOrg, "--key", "repos", "--add", "github.com/"+client+"/*", "--yes").OK(t)
				}
				ada.LoreIn(pc, "sync").OK(t)
				if c.forget {
					// as in the owner's run: the store reached through a mirror
					// redirect, the client alias through nothing lore reads
					oMirrorRules(t, ada, team.Dir, oOwner, "lore-team")
					oForgetAlias(t, ada)
				}
				if c.rules != "" {
					appendFile(t, filepath.Join(ada.Home, ".gitconfig"), c.rules)
				}
				dir := filepath.Join(ada.Home, "elsewhere", "shop") // outside every setup's folders
				if place == "setup" {
					dir = ada.OrgDir("shop")
				}
				sc := ada.CloneTo(shop, dir)
				s.Git(sc.Dir, "remote", "set-url", "origin", c.origin)
				s.Step("D-684/"+c.name+"/"+place+"/linked-client-attaches-its-folder", func() {
					ada.Lore(sc.Dir, "org", "link", "--org", oOrg, "--project", "shop", "--yes").OK(t)
					ada.Lore(sc.Dir, "memory", "add", "--body", "the shop deploys from a tag", "--to", oOrg+"/shop").OK(t)
					oAttached(t, ada.Lore(sc.Dir, "store", "list").OK(t).Stdout, oOrg, "shop")
					ada.Lore(sc.Dir, "whoami").OK(t).Says(t, "reads      "+oOrg+"/shop")
					ada.Lore(sc.Dir, "context", "--internal").OK(t).Says(t, "the shop deploys from a tag")
					ada.Lore(sc.Dir, "memory", "list").OK(t).Says(t, "the shop deploys from a tag")
					ada.Lore(sc.Dir, "memory", "add", "--body", "orders are idempotent").OK(t)
					r := ada.Lore(sc.Dir, "sync").OK(t)
					// through the attached folder, never the waiting-clone push
					// of a repo that attaches no store
					r.Says(t, "lore: pushed store "+oOrg+"/shop")
				})
			})
		}
	}
}

// oAttached fails unless the `store list` table out has the org store's
// row with folder attached (NAME URL FOLDER …).
func oAttached(t testing.TB, out, org, folder string) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) > 2 && f[0] == org && f[2] == folder {
			return
		}
	}
	t.Errorf("store %s does not attach folder %s here:\n%s", org, folder, out)
}
