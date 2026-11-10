//go:build teame2e

package teame2e

// team_e2e_synchook_test.go — `lore sync hook` on the binary under test:
// the pre-commit block stages .lore/data and refuses a broken row file
// (conflict markers, invalid JSON); an unknown hook name is refused, never
// a silent success.

import (
	"strings"
	"testing"
)

func TestB11Sync_PreCommitStagesLoreDataAndRefusesBrokenFiles(t *testing.T) {
	c := newSyncSolo(t).c
	id := mustID(t, c.MustLore("rule", "add", "--body", "use JWT for auth", "--no-live"), "rul")
	if _, ok := c.ReadFile(ruleFile(id)); !ok {
		t.Fatal("precondition: the rule's row file is on the branch")
	}
	// A partial `git add`: only the code file is staged; the hook stages
	// the lore data with it.
	c.WriteFile("src/a.go", "package src\n")
	c.Git("add", "src/a.go")
	c.Git("commit", "-q", "-m", "code")
	if files := c.Git("show", "--name-only", "--format=", "HEAD"); !strings.Contains(files, ruleFile(id)) {
		t.Fatalf("the pre-commit hook must stage .lore/data with the commit: %s", files)
	}
	for name, body := range map[string]string{
		"conflict markers": "<<<<<<< ours\n{}\n=======\n{}\n>>>>>>> theirs\n",
		"invalid JSON":     "{broken",
	} {
		t.Run(name, func(t *testing.T) {
			c.WriteFile(ruleFile(id), body)
			c.WriteFile("src/b.go", "package src // "+name+"\n")
			c.Git("add", "src/b.go")
			r := c.TryGit("commit", "-q", "-m", "more code")
			if r.Code == 0 || !strings.Contains(r.Out(), "refusing to commit broken lore files") || !strings.Contains(r.Out(), name) {
				t.Fatalf("a %s row file must refuse the commit: %s", name, r)
			}
			c.Git("checkout", "--", ruleFile(id))
		})
	}
}

// exitBadInput is the exit of class bad_input (E_INVALID_INPUT).
const exitBadInput = 20

func TestB11Sync_UnknownHookNameIsRefused(t *testing.T) {
	c := newSyncSolo(t).c
	c.Lore("sync", "hook", "pre-comit").Fails(t, exitBadInput, "E_INVALID_INPUT", "unknown hook")
	c.MustLore("sync", "hook", "refresh")
}
