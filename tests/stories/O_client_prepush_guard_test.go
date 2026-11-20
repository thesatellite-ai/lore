//go:build stories && !windows

package stories

// O_client_prepush_guard_test.go — devco:D-730 on the owner-shaped
// machine (story O): a client repo a store links by its remote, with no
// .lore/, got only lore's commit-msg block from `lore org link`, so a
// commit made with --no-verify left the clone unchecked. The link now
// installs the pre-push block too, in .git/hooks only (git status and
// every tracked file stay byte-identical), and a client repo whose hooks
// folder is committed with the repo is never written: doctor warns and
// prints the lines to add.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoryOClientRepoPrePushGuard(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	const (
		rule   = "Always replay the invoice queue against staging before a pricing change ships."
		folder = "client_invoices"
	)
	claudeMD := "# Invoices\n\nRead the README first.\n"
	invoices := s.Seed(s.Remote("github.com/acme-client/invoices"), func(dir string) {
		PlainRepo(t, dir)
		writeFile(t, filepath.Join(dir, "CLAUDE.md"), claudeMD)
	})
	portal := s.Seed(s.Remote("github.com/acme-client/portal"), func(dir string) {
		PlainRepo(t, dir)
		writeFile(t, filepath.Join(dir, "CLAUDE.md"), claudeMD)
		writeFile(t, filepath.Join(dir, ".githooks", "pre-push"), "#!/bin/sh\nexit 0\n")
	})

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ic := ada.CloneTo(invoices, ada.OrgDir("invoices"))
	state := func(dir string, git func(...string) string) string {
		b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
		h, _ := os.ReadFile(filepath.Join(dir, ".githooks", "pre-push"))
		return git("status", "--porcelain", "--untracked-files=all") + "\n" + git("ls-files", "-s") + "\n" + string(b) + string(h)
	}

	s.Step("D-730/the-link-installs-pre-push-in-git-hooks-only", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		before := state(ic.Dir, ic.Git)
		ada.Lore(ic.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t).Says(t, filepath.Join(".git", "hooks", "pre-push"))
		if after := state(ic.Dir, ic.Git); after != before {
			t.Fatalf("the link changed the client repo's files or git status:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		ada.Lore(ic.Dir, "rule", "add", "--body", rule, "--severity", "must").OK(t)
	})

	s.Step("D-730/a-no-verify-commit-quoting-the-rule-is-refused-at-push", func() {
		ic.WriteFile("CLAUDE.md", claudeMD+"\nNote: "+rule+"\n")
		ic.Git("add", "CLAUDE.md")
		ic.Git("commit", "-q", "--no-verify", "-m", "Repeat the rule")
		r := ic.TryGit("push", "-q", "origin", "HEAD")
		if r.Code == 0 {
			t.Fatalf("a --no-verify commit quoting an internal note was pushed: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "CLAUDE.md:5 ")
	})

	s.Step("D-730/a-tracked-hooks-folder-is-never-written-doctor-prints-the-lines", func() {
		oc := ada.CloneTo(portal, ada.OrgDir("portal"))
		oc.Git("config", "--local", "core.hooksPath", ".githooks")
		before := state(oc.Dir, oc.Git)
		ada.Lore(oc.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t).Says(t, "NOT checked", `lore sync hook pre-push "$@" || exit $?`)
		ada.Lore(oc.Dir, "doctor").Says(t, "NOT checked", "committed with the repo", `lore check leaks --staged --commit-msg "$1" || exit $?`, `lore sync hook pre-push "$@" || exit $?`)
		if after := state(oc.Dir, oc.Git); after != before {
			t.Fatalf("lore wrote into the client repo's committed hooks folder:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})
}
