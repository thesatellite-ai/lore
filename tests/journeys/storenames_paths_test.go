//go:build journeys && !windows

package journeys

// storenames_paths_test.go — one store, one name, whatever path prints it
// (devco D-279). Lee's org keeps a team store (org folder `org`) and a
// client store; a client repo is linked to a folder of the client store.
// The sync line named the team store "acmeteam/org" in an own repo but
// "acmeteam" in the client repo, where `lore store list` calls the client
// folder `acmeteam`; and outside any setup the local personal store was
// listed as `_me`, a name no store may have, so `lore sync` there FAILED.
// Now every sync line names a store by its own name ("<store>/<folder>"
// for a folder) and nothing else, whatever this repo's store list calls
// it (D-567: "(here: <name>)" made one place read as two). A second org's store keeps its own name and never shows in
// the first org's repos.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyStoreNamesAgreeOnEveryPath(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	two := w.Remote("github.com/acme-co/client-store")
	beta := w.Remote("github.com/beta-co/lore-beta")
	app := tt.repo[tlLee.Email]
	teamOrg := tlOrg + "/org"
	clientShown := "client-store/" + clsFolder

	w.Step("day1/team-client-store-and-client-repo", func() {
		tlStartTeam(t, lee, app.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\n    - url: "+two.SSHURL()+"\nown:\n    - github.com/acme-co/*\n")
		s.CommitAll("org file: a client store")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(parent, "store", "folder", "add", two.SSHURL(), clsFolder).OK(t)
		lee.LoreIn(app, "init").OK(t)
	})

	shopC := jPlainRepo(t, lee, w.Remote(clsClientSlug), filepath.Join(parent, "shop"))
	lee.LoreIn(shopC, "org", "link", tlOrg, "--project", clsFolder, "--store", two.SSHURL(), "--yes").OK(t)

	w.Step("day1/own-repo-sync-names-the-team-store-as-store-list-does", func() {
		lee.LoreIn(app, "memory", "add", "--body", "own repo note", "--to", teamOrg).OK(t)
		lee.LoreIn(app, "store", "list").OK(t).Says(t, teamOrg, "client-store")
		// client-store holds the store link `lore org link` wrote in the
		// client repo above, a commit with no note rows: this sync sends it
		// and says so (N-3), under the store's one name (D-279), counting
		// the commits it sent as every push line does (D-698: "sent 0"
		// under a push line of 2 commits read as nothing went out)
		lee.LoreIn(app, "sync").OK(t).Says(t, "store "+teamOrg+": pushed",
			"store client-store: pushed (sent 2, received 0, merged 0, refused 0); pushed 2 local commits with no note rows")
	})

	w.Step("day1/client-repo-sync-names-the-same-stores", func() {
		lee.LoreIn(shopC, "memory", "add", "--body", "client note").OK(t)
		lee.LoreIn(shopC, "store", "list").OK(t).Says(t, teamOrg)
		r := lee.LoreIn(shopC, "sync").OK(t)
		r.Says(t, "store "+clientShown+": pushed", "store "+teamOrg+": up to date", "store me: up to date")
		for _, l := range strings.Split(r.Out(), "\n") {
			if strings.HasPrefix(l, "store "+tlOrg+":") {
				t.Fatalf("the client repo names a store %q, which its store list gives the client folder:\n%s", tlOrg, r)
			}
		}
	})

	w.Step("day1/outside-any-repo-names-each-store-once", func() {
		lee.Lore(parent, "sync").OK(t).Says(t, "store "+tlOrg+": up to date", "store client-store: up to date", "store me: up to date")
	})

	w.Step("day1/outside-any-setup-the-personal-store-is-me", func() {
		plain := filepath.Join(lee.Home, "notes")
		if err := os.MkdirAll(plain, 0o755); err != nil {
			t.Fatal(err)
		}
		lee.Lore(plain, "store", "list").OK(t).Says(t, "me ").Never(t, "_me")
		lee.Lore(plain, "sync").OK(t).Says(t, "store me: up to date").Never(t, "_me", "FAILED")
	})

	w.Step("day2/a-second-org-keeps-its-own-name", func() {
		b := jPlainRepo(t, lee, w.Remote("github.com/beta-co/app"), filepath.Join(lee.Home, "work", "beta-app"))
		lee.LoreIn(b, "store", "init", "--name", "beta", "--url", beta.SSHURL()).OK(t)
		lee.LoreIn(b, "org", "create", "--org", "beta", "--store", beta.SSHURL(), "--own", "github.com/beta-co/*", "--key", "human:"+tlLee.Email, "--name", "Lee", "--yes").OK(t)
		lee.LoreIn(b, "init").OK(t)
		lee.LoreIn(b, "memory", "add", "--body", "beta note", "--to", "beta").OK(t)
		lee.LoreIn(b, "sync").OK(t).Says(t, "store beta").Never(t, "store "+tlOrg, "client-store")
		lee.LoreIn(app, "sync").OK(t).Says(t, "store "+teamOrg).Never(t, "store beta")
		lee.LoreIn(shopC, "sync").OK(t).Says(t, "store "+teamOrg).Never(t, "store beta")
	})
}
