//go:build stories && !windows

package stories

// O_prepush_addedlines_test.go — devco:D-728 on the owner-shaped machine
// (story O): a client repo's CLAUDE.md held hand-written lines committed
// and pushed long before the same text became a rule in the org store.
// Since D-720 a commit adding an unrelated line passed the commit hook,
// but lore's pre-push block still read the pushed file whole and refused
// the push for the old lines. The push check now reads only the lines the
// pushed commits add, with the commit check's hunk reader: the push goes
// out, and a pushed line repeating the rule (committed with --no-verify,
// so only the pre-push block sees it) is refused, naming CLAUDE.md:<line>.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoryOPrePushChecksOnlyAddedLines(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	const (
		rule   = "Always run the settlement reconciliation before tagging a billing release."
		folder = "client_billing"
		hook   = "#!/bin/sh\nif command -v lore >/dev/null 2>&1 && lore land --help >/dev/null 2>&1; then lore sync hook pre-push \"$@\" || exit $?; fi\n"
	)
	claudeMD := "# Billing\n\n" + rule + "\n"
	billing := s.Seed(s.Remote("github.com/acme-client/billing"), func(dir string) {
		PlainRepo(t, dir)
		writeFile(t, filepath.Join(dir, "CLAUDE.md"), claudeMD) // committed and pushed long ago
	})

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	bc := ada.CloneTo(billing, ada.OrgDir("billing"))

	s.Step("D-728/the-old-lines-become-a-rule", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.Lore(bc.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t)
		ada.Lore(bc.Dir, "rule", "add", "--body", rule, "--severity", "must").OK(t)
		// lore's pre-push block, as the owner's clone holds it
		p := filepath.Join(bc.Dir, ".git", "hooks", "pre-push")
		writeFile(t, p, hook)
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	})

	s.Step("D-728/an-unrelated-line-commits-and-pushes", func() {
		bc.WriteFile("CLAUDE.md", claudeMD+"\n## Build\n\nRun make test before a pull request.\n")
		bc.Git("add", "CLAUDE.md")
		if r := bc.TryGit("commit", "-q", "-m", "Document the build"); r.Code != 0 {
			t.Fatalf("an unrelated line was refused at commit: %s", r)
		}
		if r := bc.TryGit("push", "-q", "origin", "HEAD"); r.Code != 0 {
			t.Fatalf("the push of an unrelated line was refused for lines pushed long ago: %s", r)
		}
	})

	s.Step("D-728/a-pushed-line-repeating-the-rule-is-refused-at-its-line", func() {
		body, _ := bc.ReadFile("CLAUDE.md")
		bc.WriteFile("CLAUDE.md", body+"\nNote: "+rule+"\n")
		bc.Git("add", "CLAUDE.md")
		bc.Git("commit", "-q", "--no-verify", "-m", "Repeat the rule")
		r := bc.TryGit("push", "-q", "origin", "HEAD")
		if r.Code == 0 {
			t.Fatalf("a pushed line repeating the rule went out: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "CLAUDE.md:9 ").Never(t, "CLAUDE.md:3 ")
	})
}
