//go:build stories && !windows

package stories

// O_prepush_linkmsg_test.go — devco:D-904 (public issue #127) on the
// owner-shaped machine (story O), replaying the issue's script: a client
// repo linked with `lore org link` (commit-msg and pre-push blocks), a PR
// branch whose commit has a 12-word subject is pushed, a task is linked
// to that commit with `lore link add` (which copies the commit's message
// into the commit_links row), main moves on, the branch is rebased and
// force-pushed. The pre-push text rule refused the rebased commit as a
// leak of the link row; the row's message is the commit's own text, so
// the leak index no longer reads it: the force-push and an amend keeping
// the message go out, and the same words in a memory still block.

import (
	"testing"
)

func TestStoryOPrePushRebasedLinkedCommit(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	const (
		folder  = "client_checkout"
		subject = "Show the order received banner after the customer submits the checkout form"
		note    = "The client wants the order received banner hidden until the spring launch is announced"
	)
	checkout := s.Seed(s.Remote("github.com/acme-client/checkout"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	cc := ada.CloneTo(checkout, ada.OrgDir("checkout"))

	s.Step("D-904/a-linked-pr-commit-is-pushed", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.Lore(cc.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t)
		cc.Git("checkout", "-q", "-b", "pr-a")
		cc.WriteFile("banner.go", "package main\n\nfunc banner() {}\n")
		cc.Git("add", "banner.go")
		if r := cc.TryGit("commit", "-q", "-m", subject); r.Code != 0 {
			t.Fatalf("the PR commit was refused: %s", r)
		}
		if r := cc.TryGit("push", "-q", "origin", "pr-a"); r.Code != 0 {
			t.Fatalf("the PR branch push was refused: %s", r)
		}
		task := oAddedID(t, ada.Lore(cc.Dir, "task", "add", "--title", "Banner after submit", "--commitment", "accepted", "--to", oOrg, "--json").OK(t))
		if task == "" {
			t.Fatal("task add printed no id")
		}
		ada.Lore(cc.Dir, "link", "add", "--entity", task, "--commit", "HEAD", "--to", oOrg, "--yes").OK(t)
		ada.Lore(cc.Dir, "link", "list", "--entity", task, "--json").OK(t).Says(t, subject)
	})

	s.Step("D-904/the-rebased-commit-force-pushes", func() {
		cc.Git("checkout", "-q", "main")
		cc.WriteFile("README.md", "# checkout\n")
		cc.Git("add", "README.md")
		cc.Git("commit", "-q", "-m", "Merge an unrelated change")
		if r := cc.TryGit("push", "-q", "origin", "main"); r.Code != 0 {
			t.Fatalf("the push of main was refused: %s", r)
		}
		cc.Git("checkout", "-q", "pr-a")
		cc.Git("rebase", "-q", "main")
		if r := cc.TryGit("push", "-q", "--force-with-lease", "origin", "pr-a"); r.Code != 0 {
			t.Fatalf("the rebased commit was refused as a leak of its own commit link: %s", r)
		}
	})

	s.Step("D-904/an-amend-keeping-the-message-commits-and-pushes", func() {
		cc.WriteFile("banner.go", "package main\n\nfunc banner() { _ = 1 }\n")
		cc.Git("add", "banner.go")
		if r := cc.TryGit("commit", "-q", "--amend", "--no-edit"); r.Code != 0 {
			t.Fatalf("the commit-msg check refused the amended commit's own message: %s", r)
		}
		if r := cc.TryGit("push", "-q", "--force-with-lease", "origin", "pr-a"); r.Code != 0 {
			t.Fatalf("the amended commit was refused at push: %s", r)
		}
	})

	s.Step("D-904/the-same-guard-still-blocks-a-note-a-person-wrote", func() {
		ada.Lore(cc.Dir, "memory", "add", "--body", note, "--to", oOrg).OK(t)
		cc.WriteFile("banner.go", "package main\n\nfunc banner() { _ = 2 }\n")
		cc.Git("add", "banner.go")
		cc.Git("commit", "-q", "--no-verify", "-m", note)
		r := cc.TryGit("push", "-q", "origin", "pr-a")
		if r.Code == 0 {
			t.Fatalf("a commit message quoting an internal memory was pushed: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "message of commit")
	})
}
