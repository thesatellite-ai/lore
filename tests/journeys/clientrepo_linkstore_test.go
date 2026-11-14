//go:build journeys && !windows

package journeys

// clientrepo_linkstore_test.go — a client repo whose notes live in the
// org's second store (devco:N-9, devco:N-10). Lee links the client repo
// to a folder of the client store; a teammate's clone of it then:
//   - reads whoami's guards-off line with the folder filled in (it named
//     `--project <folder>` though lore knew the folder);
//   - runs `lore org link` without --store, as that hint says: every line
//     names the client store (two lines named the org's first store, then
//     "✓ linked to" the client store);
//   - runs `lore init`: the refusal names the "# lore" block init appends
//     to the client's tracked .gitignore (it was written but not named),
//     the block holds only lore's local files, and the first install with
//     --yes does not say the leak guard was "off" or "commits made
//     meanwhile were not checked" (only the link's commit guard was there).

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	clsClientSlug = "github.com/client-co/shop"
	clsFolder     = "client_shop"
)

func TestJourneyClientRepoInSecondStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	two := w.Remote("github.com/acme-co/client-store")
	var other *teamharness.Clone

	w.Step("day1/link-the-client-repo-to-the-client-store", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\n    - url: "+two.SSHURL()+"\nown:\n    - github.com/acme-co/*\n")
		s.CommitAll("org file: a client store")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(parent, "store", "folder", "add", two.SSHURL(), clsFolder).OK(t)
		shop := jPlainRepo(t, lee, w.Remote(clsClientSlug), filepath.Join(parent, "shop"))
		lee.LoreIn(shop, "org", "link", tlOrg, "--project", clsFolder, "--store", two.SSHURL(), "--yes").OK(t)
		other = lee.Clone(w.Remote(clsClientSlug), "shop-two")
		lee.MoveClone(t, other, filepath.Join(parent, "shop-two"))
	})

	w.Step("day1/whoami-hint-names-the-folder", func() {
		r := lee.LoreIn(other, "whoami").OK(t)
		if strings.Contains(r.Out(), "guards OFF") {
			r.Says(t, "--project "+clsFolder+" --yes").Never(t, "<folder>")
		}
	})

	w.Step("day1/link-without-store-names-the-client-store", func() {
		r := lee.LoreIn(other, "org", "link", "--org", tlOrg, "--project", clsFolder, "--yes").OK(t)
		storeTwo := strings.TrimSuffix(strings.TrimPrefix(two.SSHURL(), "git@"), ".git")
		for _, l := range strings.Split(r.Out(), "\n") {
			if strings.Contains(l, clsFolder+"/") && !strings.Contains(l, "client-store") {
				t.Fatalf("a line of org link names another store than the client store (%s): %q\n%s", storeTwo, l, r)
			}
		}
	})

	w.Step("day1/init-names-its-gitignore-edit-and-says-no-guard-was-off", func() {
		jWrite(t, filepath.Join(other.Dir, ".gitignore"), "node_modules/\n")
		other.CommitAll("their ignore file")
		// the refusal names every file init would write, the .gitignore block included
		lee.LoreIn(other, "init", "--non-interactive", "--name=shop").Fails(t, 26, ".gitignore", ".gitattributes")
		r := lee.LoreIn(other, "init", "--yes", "--non-interactive", "--name=shop").OK(t)
		// a first install: no hook of lore was ever there to be "off"
		r.Never(t, "leak guard off", "commits made meanwhile")
		got, _ := other.ReadFile(".gitignore")
		theirs, block, ok := strings.Cut(got, "# lore\n")
		if !ok || theirs != "node_modules/\n\n" {
			t.Fatalf("the client's .gitignore lost its own lines or has no \"# lore\" block:\n%s", got)
		}
		for _, l := range strings.Split(strings.TrimSpace(block), "\n") {
			if !strings.HasPrefix(l, ".lore/") {
				t.Fatalf("init's .gitignore block holds a line that is not one of lore's local files: %q\n%s", l, got)
			}
		}
		if st := other.Status(); strings.Contains(st, ".lore/lock") || strings.Contains(st, ".lore/state") {
			t.Fatalf("lore's local files show as untracked in the client repo:\n%s", st)
		}
	})
}
