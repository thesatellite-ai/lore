//go:build stories && !windows

package stories

// O_prepush_rebase_test.go — public issue #128 (devco:D-905) on the
// owner-shaped machine (story O), the issue's own script as steps: a PR
// branch adds a line of UI copy and is pushed; later an internal decision
// quotes that line verbatim; main moves on, the branch is rebased onto it
// and force-pushed. The rebase makes every commit of the branch new, so
// lore's pre-push block refused the push for copy.txt:1, a line byte-
// identical on the remote ref the push replaces. Text the remote already
// has is not new: the push goes out, and a line the push really adds that
// quotes the decision is still refused, named at its own line.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoryOPrePushAfterRebase(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	const (
		copyLine = "Your order has been received and a support agent will contact you within two business days."
		folder   = "client_app"
		hook     = "#!/bin/sh\nif command -v lore >/dev/null 2>&1 && lore land --help >/dev/null 2>&1; then lore sync hook pre-push \"$@\" || exit $?; fi\n"
	)
	app := s.Seed(s.Remote("github.com/acme-client/app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ac := ada.CloneTo(app, ada.OrgDir("app"))

	s.Step("D-905/link-the-client-repo", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.Lore(ac.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t)
		p := filepath.Join(ac.Dir, ".git", "hooks", "pre-push") // lore's pre-push block, as the owner's clone holds it
		writeFile(t, p, hook)
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	})

	s.Step("D-905/a-pr-branch-pushes-the-copy", func() {
		ac.Git("switch", "-q", "-c", "pr-b")
		ac.WriteFile("copy.txt", copyLine+"\n")
		ac.Git("add", "copy.txt")
		ac.Git("commit", "-q", "-m", "Add the submit copy")
		if r := ac.TryGit("push", "-q", "origin", "pr-b"); r.Code != 0 {
			t.Fatalf("the first push of the copy was refused: %s", r)
		}
	})

	s.Step("D-905/a-decision-quotes-the-copy", func() {
		ada.Lore(ac.Dir, "decision", "add", "--title", "Banner wording approved",
			"--body", "Approved wording for the banner: \""+copyLine+"\" Do not change it without sign-off.").OK(t)
	})

	s.Step("D-905/rebase-and-force-push-goes-out", func() {
		ac.Git("switch", "-q", "main")
		ac.WriteFile("README.md", "# app\n")
		ac.Git("add", "README.md")
		ac.Git("commit", "-q", "-m", "Merge an unrelated change")
		if r := ac.TryGit("push", "-q", "origin", "main"); r.Code != 0 {
			t.Fatalf("the unrelated push of main was refused: %s", r)
		}
		ac.Git("switch", "-q", "pr-b")
		ac.Git("rebase", "-q", "main")
		if r := ac.TryGit("push", "-q", "--force-with-lease", "origin", "pr-b"); r.Code != 0 {
			t.Fatalf("the force-push after a rebase was refused for a line already on the ref it replaces: %s", r)
		}
	})

	s.Step("D-905/a-line-the-push-adds-is-still-refused", func() {
		ac.WriteFile("copy.txt", copyLine+"\nBanner: "+copyLine+"\n")
		ac.Git("add", "copy.txt")
		ac.Git("commit", "-q", "--no-verify", "-m", "Repeat the copy")
		r := ac.TryGit("push", "-q", "origin", "pr-b")
		if r.Code == 0 {
			t.Fatalf("a pushed line quoting the decision went out: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "copy.txt:2 ").Never(t, "copy.txt:1 ")
	})
}
