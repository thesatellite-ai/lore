//go:build stories && !windows

package stories

// O_deleteonly_leak_test.go — devco:D-946 (issue #156) on the owner-shaped
// machine (story O): in a client repo with the leak guard on, a commit
// that only deletes lines of an already-pushed file was refused by the
// commit hook, and the same commit made with --no-verify by the pre-push
// block, naming an unchanged line of that file that matched a rule in the
// org store. The hunk reader dropped every file whose hunks add no line,
// and a file it does not describe is read whole. A delete-only file, a
// mode-only change and a pure rename now add nothing to check; a line the
// commit adds is still checked and refused at its own line number.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoryODeleteOnlyCommitPassesLeakGuard(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	const (
		rule   = "Deploy credentials (SERVICE_ACCESS_KEY_ID, SERVICE_SECRET_ACCESS_KEY) live in the vault, never in a repo."
		folder = "client_ledger"
		hook   = "#!/bin/sh\nif command -v lore >/dev/null 2>&1 && lore land --help >/dev/null 2>&1; then lore sync hook pre-push \"$@\" || exit $?; fi\n"
	)
	// lines 4-5 are 8 words in a row of the rule: "service access key id
	// service secret access key"
	env := "# copy to .env and fill in\nAPP_PORT=\nLOG_LEVEL=\nSERVICE_ACCESS_KEY_ID=\nSERVICE_SECRET_ACCESS_KEY=\nREGION=\nFEATURE_FLAGS=\nCACHE_TTL=\n"
	ledger := s.Seed(s.Remote("github.com/acme-client/ledger"), func(dir string) {
		PlainRepo(t, dir)
		writeFile(t, filepath.Join(dir, ".env.example"), env) // committed and pushed long ago
		writeFile(t, filepath.Join(dir, "deploy.sh"), "echo deploy\n")
		writeFile(t, filepath.Join(dir, "NOTES.md"), "notes\n")
	})

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	lc := ada.CloneTo(ledger, ada.OrgDir("ledger"))

	s.Step("D-946/the-variable-names-become-part-of-a-rule", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.Lore(lc.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t)
		ada.Lore(lc.Dir, "rule", "add", "--body", rule, "--severity", "must").OK(t)
		p := filepath.Join(lc.Dir, ".git", "hooks", "pre-push")
		writeFile(t, p, hook)
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	})

	deleteOnly := "# copy to .env and fill in\nSERVICE_ACCESS_KEY_ID=\nSERVICE_SECRET_ACCESS_KEY=\nREGION=\n"
	s.Step("D-946/a-delete-only-commit-passes-check-commit-and-push", func() {
		lc.WriteFile(".env.example", deleteOnly)
		lc.Git("add", ".env.example")
		ada.Lore(lc.Dir, "check", "leaks", "--explain").OK(t)
		if r := lc.TryGit("commit", "-q", "-m", "Drop unused settings"); r.Code != 0 {
			t.Fatalf("a commit that only deletes lines was refused: %s", r)
		}
		if r := lc.TryGit("push", "-q", "origin", "HEAD"); r.Code != 0 {
			t.Fatalf("the push of a delete-only commit was refused: %s", r)
		}
	})

	s.Step("D-946/a-delete-only-commit-made-with-no-verify-pushes", func() {
		lc.WriteFile(".env.example", "# copy to .env and fill in\nSERVICE_ACCESS_KEY_ID=\nSERVICE_SECRET_ACCESS_KEY=\n")
		lc.Git("add", ".env.example")
		lc.Git("commit", "-q", "--no-verify", "-m", "Drop the region")
		if r := lc.TryGit("push", "-q", "origin", "HEAD"); r.Code != 0 {
			t.Fatalf("the pre-push block refused a delete-only commit: %s", r)
		}
	})

	s.Step("D-946/a-mode-only-change-and-a-pure-rename-pass", func() {
		if err := os.Chmod(filepath.Join(lc.Dir, "deploy.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
		lc.Git("mv", ".env.example", "env.sample")
		lc.Git("add", "-A")
		ada.Lore(lc.Dir, "check", "leaks").OK(t)
		if r := lc.TryGit("commit", "-q", "-m", "Rename the sample env file"); r.Code != 0 {
			t.Fatalf("a mode-only change and a pure rename were refused: %s", r)
		}
		if r := lc.TryGit("push", "-q", "origin", "HEAD"); r.Code != 0 {
			t.Fatalf("the push of a mode-only change and a pure rename was refused: %s", r)
		}
	})

	s.Step("D-946/an-added-line-is-still-checked-at-its-own-line", func() {
		body, _ := lc.ReadFile("NOTES.md")
		lc.WriteFile("NOTES.md", body+"Deploy credentials SERVICE_ACCESS_KEY_ID SERVICE_SECRET_ACCESS_KEY live in the vault\n")
		lc.Git("add", "NOTES.md")
		ada.Lore(lc.Dir, "check", "leaks").Fails(t, 19, "E_LEAK_BLOCKED", "NOTES.md:2 ").Never(t, "env.sample:")
		lc.Git("reset", "-q", "--hard", "HEAD")
	})
}
