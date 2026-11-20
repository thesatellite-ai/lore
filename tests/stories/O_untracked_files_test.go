//go:build stories && !windows

package stories

// O_untracked_files_test.go — devco D-732 on the owner-shaped machine
// (story O): an org repo whose hooks folder is committed (core.hooksPath=
// .githooks, the team's own pre-push gate) and that has a .claude folder.
// lore created .githooks/commit-msg and .githooks/post-commit and wrote
// its agent hooks into .claude/settings.json: every `git status` listed
// them. The cell: after init is committed, sync, doctor --fix and
// setup --agent-hooks leave nothing in git status but .lore/ rows, the
// team's hook is byte for byte as it was, and the agent hooks are in
// .claude/settings.local.json.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryONoUntrackedFiles(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	const gate = "#!/bin/sh\necho team gate\n"
	gated := s.Seed(s.Remote("github.com/"+oOwner+"/gated"), func(dir string) {
		PlainRepo(t, dir)
		writeExec(t, filepath.Join(dir, ".githooks", "pre-push"), gate)
		writeFile(t, filepath.Join(dir, ".claude", "commands", "review.md"), "review the diff\n")
	})

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	gc := ada.CloneTo(gated, ada.OrgDir("gated"))
	oSetupFlow(t, ada, gc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	gc.Git("config", "core.hooksPath", ".githooks")

	s.Step("D-732/init-committed", func() {
		ada.Lore(gc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/gated", "--yes").OK(t)
		ada.Lore(gc.Dir, "init", "--yes").OK(t)
		gc.Git("add", "-A")
		gc.TryGit("commit", "-q", "--no-verify", "-m", "lore").OK(t)
	})

	s.Step("D-732/set-up-commands-leave-nothing-untracked", func() {
		ada.Lore(gc.Dir, "sync")
		ada.Lore(gc.Dir, "doctor", "--fix")
		ada.Lore(gc.Dir, "setup", "--agent-hooks", "--yes").OK(t).Says(t, ".claude/settings.local.json")
		var stray []string
		for _, l := range strings.Split(gc.Git("status", "--porcelain", "--untracked-files=all"), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, ".lore/") {
				stray = append(stray, l)
			}
		}
		if len(stray) > 0 {
			t.Fatalf("lore left files in git status: %q", stray)
		}
		if b, err := os.ReadFile(filepath.Join(gc.Dir, ".githooks", "pre-push")); err != nil || string(b) != gate {
			t.Fatalf("the team's pre-push changed: %q %v", b, err)
		}
		if _, err := os.Stat(filepath.Join(gc.Dir, ".claude", "settings.json")); !os.IsNotExist(err) {
			t.Fatalf("lore wrote .claude/settings.json without the team's opt-in (%v)", err)
		}
	})
}
