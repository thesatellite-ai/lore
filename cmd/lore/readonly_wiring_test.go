package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestReadOnlyCommandWiresGit: OWNERQ-9 / G-DECIDE-9 (a): the git wiring
// (merge driver config, hook blocks) runs after every command in a git
// checkout, read-only commands included, so a read-only CI clone is wired
// by its first lore command (it writes git's own files, never rows).
func TestReadOnlyCommandWiresGit(t *testing.T) {
	asPerson(t)
	root := envScopeRepo(t) // runs `lore init`
	gitIn(t, root, "config", "--local", "--remove-section", "merge.lore")
	driver := func() string {
		out, _ := exec.Command("git", "-C", root, "config", "--local", "--get", "merge.lore.driver").Output()
		return strings.TrimSpace(string(out))
	}
	if driver() != "" {
		t.Fatal("setup: merge.lore.driver still set")
	}
	t.Setenv("LORE_READ_ONLY", "1") // read-only mode: still wired
	for _, args := range [][]string{{"task", "list"}, {"memory", "list"}, {"doctor"}} {
		if code, out, errOut := runCaptured(t, args...); code != 0 && args[0] != "doctor" {
			t.Fatalf("lore %v: exit %d\n%s\n%s", args, code, out, errOut)
		}
		if !strings.Contains(driver(), "merge-driver") {
			t.Fatalf("lore %v left merge.lore.driver unset", args)
		}
		gitIn(t, root, "config", "--local", "--remove-section", "merge.lore")
	}
}
