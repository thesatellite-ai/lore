//go:build journeys && !windows

package journeys

// r7torg_test.go — an org's life after the first year (devco round 7,
// track T): a repo the org moves to another host owner, an org rename, a
// client store retired.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// r7OwnPattern adds pattern to the org's own: list in its store (an
// admin's hand edit of lore-org.yaml) and refreshes the lead's entry.
func r7OwnPattern(t *testing.T, tt *tlTeam, m *Machine, pattern string) {
	t.Helper()
	parent := filepath.Join(m.Home, "code")
	s := m.Clone(tt.store, "team-store-own")
	f := filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile))
	body, ok := s.ReadFile(jManyOrgFile)
	if !ok {
		t.Fatalf("the team store has no %s", jManyOrgFile)
	}
	if strings.Contains(body, "own:") {
		body = strings.Replace(body, "own:\n", "own:\n    - "+pattern+"\n", 1)
	} else {
		body += "own:\n    - " + pattern + "\n"
	}
	jWrite(t, f, body)
	s.CommitAll("the org's own repos")
	s.Git("push", "-q", "origin", "HEAD:main")
	m.Lore(parent, "org", "refresh", tlOrg).OK(t)
	m.Lore(parent, "sync").OK(t)
}

// T-17: the org's own app moves to another owner on the git host. lore
// keeps it an own repo, names the move and the fix, and never blocks a
// plain code commit.
func TestJourneyR7MovedOwnRepo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	c := tt.repo[tlLee.Email]
	w.Step("day1/own-app-set-up", func() {
		tlStartTeam(t, lee, c.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		r7OwnPattern(t, tt, lee, "github.com/acme-co/*")
		tt.lore(tlLee, "init").OK(t)
		tt.lore(tlLee, "memory", "add", "--body", "the app deploys on mondays").OK(t)
		c.CommitAll("lore init")
		tt.lore(tlLee, "whoami").OK(t).Says(t, "own repo")
	})
	w.Step("month2/app-moves-to-another-owner", func() {
		c.Git("remote", "set-url", "origin", "git@github.com:other-co/app.git")
		tt.lore(tlLee, "whoami").OK(t).Says(t, "own repo", "github.com/other-co/app matches none of "+tlOrg+"'s own: patterns",
			"github.com/other-co/*", "lore org refresh --org "+tlOrg, "lore org link --org "+tlOrg+" --project <folder> --yes").Never(t, "client repo")
		tt.lore(tlLee, "memory", "add", "--body", "still ours").OK(t)
		jWrite(t, filepath.Join(c.Dir, "README.md"), "# app\nmoved\n")
		if r := c.TryGit("commit", "-qam", "docs: readme"); r.Code != 0 {
			t.Fatalf("a plain code commit in the moved own repo was refused:\n%s%s", r.Stdout, r.Stderr)
		}
		if v := c.TryGit("config", "--local", "--get", "lore.clientrepo"); v.Code == 0 {
			t.Errorf("the moved own repo was marked a client repo: %s", v.Stdout)
		}
	})
}

// T-5 / T-22: the lead makes a store for one client with a name of their
// own (store init --name), adds it to the org and links the client repo.
// The name works in the client repo; store list shows the store once; when
// the contract ends an admin retires it with one command, a member cannot,
// and the notes stay in the store.
func TestJourneyR7ClientStoreNameAndRetire(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	cs := w.Remote("github.com/acme-co/clientco-notes")
	app := tt.repo[tlLee.Email]
	var shop *teamharness.Clone
	w.Step("day1/client-store-with-a-name", func() {
		tlStartTeam(t, lee, app.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.LoreIn(app, "store", "init", "--name", "clientco", "--url", cs.SSHURL(), "--yes").OK(t)
		lee.LoreIn(app, "org", "store", "add", "--org", tlOrg, "--url", cs.SSHURL(), "--push", "--yes").OK(t).
			Says(t, "named clientco")
		shop = jPlainRepo(t, lee, w.Remote("github.com/client-co/shop"), filepath.Join(parent, "shop"))
		lee.LoreIn(shop, "org", "link", "--org", tlOrg, "--project", "shop", "--store", cs.SSHURL(), "--yes").OK(t)
		lee.LoreIn(shop, "memory", "add", "--to", "clientco", "--body", "the shop deploys on fridays").OK(t)
		lee.LoreIn(shop, "memory", "list").OK(t).Says(t, "the shop deploys on fridays")
		list := lee.LoreIn(app, "store", "list").OK(t)
		if n := strings.Count(list.Stdout, "clientco-notes.git"); n != 1 {
			t.Errorf("store list shows the client store %d times, want once:\n%s", n, list.Stdout)
		}
	})
	w.Step("month9/contract-ends-store-retired", func() {
		lee.Lore(parent, "org", "store", "remove", "--org", tlOrg, "--url", tt.store.SSHURL(), "--yes").
			Refused(t).Says(t, "org folder", "never retired")
		lee.Lore(parent, "org", "store", "remove", "--org", tlOrg, "--url", cs.SSHURL(), "--push", "--yes").OK(t).
			Says(t, "retired store github.com/acme-co/clientco-notes", "every note in it are kept")
		lee.Lore(parent, "org", "store", "remove", "--org", tlOrg, "--url", cs.SSHURL(), "--yes").
			Fails(t, 27, "lists no store")
		if org, ok := tt.store.Read("refs/heads/main", jManyOrgFile); !ok || strings.Contains(org, "clientco-notes") {
			t.Errorf("the org's lore-org.yaml still lists the retired store:\n%s", org)
		}
		if !cs.Has("refs/heads/main") {
			t.Error("retiring the store touched the client store itself")
		}
	})
}
