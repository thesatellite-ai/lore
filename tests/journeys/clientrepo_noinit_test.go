//go:build journeys && !windows

package journeys

// clientrepo_noinit_test.go — a client repo the org's repos: pattern
// claims, before anyone linked it (round 3 client3 group). Lee's org lists
// github.com/client-co/* as client repos. In a fresh clone of one:
//   - a refused write names the org link, never `lore init` (I-13: the
//     hint said "lore init sets this repo up", and following it wrote
//     .lore/, .gitattributes, hooks and lore's pointer into the client's
//     CLAUDE.md/AGENTS.md, after which the write still failed);
//   - lore init asks first (E_NEEDS_CONFIRM) and writes nothing;
//   - after `lore org link`, lore where lists the clone as using its store
//     caches, not "(gone: moved or deleted …)" (I-12).

import (
	"path/filepath"
	"testing"
)

const (
	cnSlug   = "github.com/client-co/shop"
	cnFolder = "client_shop"
)

func TestJourneyClientRepoNoInit(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	remote := w.Remote(cnSlug)

	w.Step("day1/an-unlinked-client-repo-never-hints-init", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\nrepos:\n    - github.com/client-co/*\n")
		s.CommitAll("the org's client repos")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", cnFolder).OK(t)
		lee.Lore(parent, "sync").OK(t)

		shop := jPlainRepo(t, lee, remote, filepath.Join(parent, "shop"))
		lee.LoreIn(shop, "whoami").OK(t).Says(t, "client repo")
		lee.LoreIn(shop, "memory", "add", "--body", "the shop deploys on fridays").
			Fails(t, 19, "lore org link --org "+tlOrg+" --project <folder>").Never(t, "lore init sets")
		lee.LoreIn(shop, "doctor").Never(t, "lore init sets")
		lee.LoreIn(shop, "init").Fails(t, 26, "client repo of "+tlOrg, "lore init --yes")
		if st := shop.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("a refused init left files in the client repo:\n%s", st)
		}

		lee.LoreIn(shop, "org", "link", tlOrg, "--project", cnFolder, "--yes").OK(t)
		lee.LoreIn(shop, "memory", "add", "--body", "the shop deploys on fridays").OK(t).Says(t, cnFolder)
		lee.LoreIn(shop, "where").OK(t).Says(t, shop.Dir).Never(t, "gone: moved or deleted")
		if st := shop.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("the linked client repo must hold nothing of lore's:\n%s", st)
		}
	})

}
