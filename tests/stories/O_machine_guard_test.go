//go:build stories && !windows

package stories

// O_machine_guard_test.go — devco H-119 (org-setup item 12) on the
// owner-shaped machine (story O): the link and `lore doctor --fix` guard
// the clones they run in, so a plain `git clone` of a client repo where
// lore never ran committed a store rule unchecked. `lore setup guard
// install` (opt-in, never on by default) points the global core.hooksPath
// at lore's dispatchers: such a clone is checked too, each repo's own
// hooks still run first, and `lore setup guard remove` puts the global
// core.hooksPath back exactly (here: a hook manager's ~/ folder).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoryOMachineGuard(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	const (
		rule   = "Always replay the ledger export against staging before a currency change ships."
		folder = "client_ledger"
	)
	agentsMD := "# Ledger\n\nRead the README first.\n"
	ledger := s.Seed(s.Remote("github.com/acme-client/ledger"), func(dir string) {
		PlainRepo(t, dir)
		writeFile(t, filepath.Join(dir, "AGENTS.md"), agentsMD)
	})

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	lc := ada.CloneTo(ledger, ada.OrgDir("ledger"))
	ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
	ada.Lore(lc.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t)
	ada.Lore(lc.Dir, "rule", "add", "--body", rule, "--severity", "must").OK(t)

	// a hook manager's global folder, written as ~/ (git expands it)
	managerHooks := filepath.Join(ada.Home, ".manager-hooks")
	writeFile(t, filepath.Join(managerHooks, "pre-commit"), "#!/bin/sh\necho manager-pre-commit-ran >&2\n")
	if err := os.Chmod(filepath.Join(managerHooks, "pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	pc.Git("config", "--global", "core.hooksPath", "~/.manager-hooks")

	quote := func(dir string, git func(...string) string) {
		writeFile(t, filepath.Join(dir, "AGENTS.md"), agentsMD+"\nNote: "+rule+"\n")
		git("add", "AGENTS.md")
	}

	s.Step("H-119/a-fresh-clone-lore-never-ran-in-is-unguarded-without-it", func() {
		fc := ada.CloneTo(ledger, ada.OrgDir("ledger-fresh"))
		quote(fc.Dir, fc.Git)
		if r := fc.TryGit("commit", "-q", "-m", "Repeat the rule"); r.Code != 0 {
			t.Fatalf("with no machine guard a fresh clone has no lore hook, the commit should go through: %s", r)
		}
	})

	s.Step("H-119/install-is-opt-in-and-a-dry-run-writes-nothing", func() {
		ada.Lore(pc.Dir, "setup", "guard", "install", "--dry-run").OK(t).Says(t, "would install", "nothing written")
		if got := pc.Git("config", "--global", "--get", "core.hooksPath"); got != "~/.manager-hooks" {
			t.Fatalf("a dry run changed the global core.hooksPath to %q", got)
		}
		ada.Lore(pc.Dir, "setup", "guard", "install").OK(t).Says(t, "machine guard installed", `"~/.manager-hooks"`, "lore setup guard remove")
	})

	s.Step("H-119/a-fresh-clone-is-guarded-and-the-previous-hooks-still-run", func() {
		fc := ada.CloneTo(ledger, ada.OrgDir("ledger-fresh2"))
		quote(fc.Dir, fc.Git)
		r := fc.TryGit("commit", "-q", "-m", "Repeat the rule")
		if r.Code == 0 {
			t.Fatalf("the machine guard let a commit quoting a store rule through in a clone lore never ran in: %s", r)
		}
		r.Says(t, "manager-pre-commit-ran", "E_LEAK_BLOCKED")
		fc.WriteFile("AGENTS.md", agentsMD+"\nPlain text.\n")
		fc.Git("add", "AGENTS.md")
		if r := fc.TryGit("commit", "-q", "-m", "Plain change"); r.Code != 0 {
			t.Fatalf("the machine guard refused a commit with no internal note: %s", r)
		}
	})

	s.Step("H-119/doctor-and-whoami-say-the-machine-guard-covers-the-clone", func() {
		fc := ada.CloneTo(ledger, ada.OrgDir("ledger-fresh3"))
		ada.Lore(fc.Dir, "doctor").Says(t, "machine guard").Never(t, "leak guard off")
	})

	s.Step("H-119/remove-restores-the-global-core-hookspath-exactly", func() {
		ada.Lore(pc.Dir, "setup", "guard", "remove").OK(t).Says(t, "removed the machine guard", `"~/.manager-hooks" again`)
		if got := pc.Git("config", "--global", "--get", "core.hooksPath"); got != "~/.manager-hooks" {
			t.Fatalf("remove left the global core.hooksPath at %q, want ~/.manager-hooks", got)
		}
		ada.Lore(pc.Dir, "setup", "guard", "remove").OK(t).Says(t, "not installed")
	})
}
