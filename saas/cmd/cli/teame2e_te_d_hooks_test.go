//go:build teame2e

package main

// later.md 607 (b11-sync ports) on cmd/lore with two clones: the git
// wiring a teammate's first lore command installs guards her commits, and
// a broad repo ignore rule cannot hide lore's row files from the team.

import (
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// teDSoloPair is bob's clone of the loose repo and carol, both on no-org
// machines; carol clones once bob has pushed.
func teDSoloPair(t *testing.T, caps ...th.Cap) (*th.Clone, *th.Actor) {
	t.Helper()
	w := newTeamWorld(t, caps...)
	r := w.Remote(slugLoose)
	bob := w.Human("bob", "bob@solo.example")
	bob.WriteConfig(teDSoloConfig("human:bob@solo.example", "Bob Roe", "bob"))
	carol := w.Human("carol", "carol@solo.example")
	carol.WriteConfig(teDSoloConfig("human:carol@solo.example", "Carol Poe", "carol"))
	return bob.Clone(r, "tool"), carol
}

// TestE2E_PreCommitBlocksHandAddedSecret, ported: carol's clone gets the
// pre-commit guard from her first lore command; a secret she pastes into
// a row file by hand is refused at commit and never reaches the remote.
func TestTeD_PreCommitBlocksHandAddedSecretInTeammateClone(t *testing.T) {
	bc, carol := teDSoloPair(t)
	bc.MustLore("init", "--non-interactive", "--name=tool")
	id := mustID(t, bc.MustLore("rule", "add", "--body", "clean rule: nothing secret here"), "rul")
	bc.CommitAll("lore")
	bc.AdminPush("origin", "main")

	cc := carol.Clone(bc.Remote, "tool")
	cc.MustLore("rule", "list").Says(t, "nothing secret here")
	rel := dataDir + "/" + tblRules + "/" + id + ".json"
	body, ok := cc.ReadFile(rel)
	if !ok || !strings.Contains(body, "nothing secret here") {
		t.Fatalf("carol's clone has bob's rule file: %q", body)
	}
	key := "AKIA" + "IOSFODNN7EXAMPLE" // the AWS documentation example key, built at run time
	cc.WriteFile(rel, strings.Replace(body, "nothing secret here", "key "+key, 1))
	cc.Git("add", "-A")
	headBefore := cc.Head()
	if res := cc.TryGit("commit", "-qm", "leak"); res.Code == 0 {
		t.Fatalf("a hand-pasted secret must be refused at commit: %s", res)
	}
	if cc.Head() != headBefore {
		t.Fatal("the refused commit made no commit")
	}
	for _, f := range bc.Remote.AllObjectsContain(key) {
		t.Fatalf("the secret reached the remote: %s", f)
	}
}

// TestE2E_RepoIgnoreRuleCannotHideData, ported: a repo whose .gitignore
// ignores every *.json still commits lore's row files (init re-includes
// .lore/data), a teammate's fresh clone reads them, and her commands
// leave the committed .gitignore alone.
func TestTeD_RepoIgnoreRuleCannotHideDataFromTeammate(t *testing.T) {
	bc, carol := teDSoloPair(t)
	bc.WriteFile(".gitignore", "_*\n*.json\nsnapshots/\n")
	bc.CommitAll("repo ignore rules")
	bc.MustLore("init", "--non-interactive", "--name=tool")
	bc.MustLore("rule", "add", "--body", "survives a broad ignore rule")
	bc.CommitAll("lore")
	if out := bc.Git("ls-files", dataDir+"/_meta.json"); out == "" {
		t.Fatal("_meta.json is committed despite the _* rule")
	}
	if out := bc.Git("ls-files", dataDir+"/"+tblRules); out == "" {
		t.Fatal("the rule row file is committed despite the *.json rule")
	}
	bc.AdminPush("origin", "main")

	cc := carol.Clone(bc.Remote, "tool")
	cc.MustLore("rule", "list").Says(t, "survives a broad ignore rule")
	if out := cc.Git("status", "--porcelain", "--", ".gitignore"); out != "" {
		t.Fatalf("carol's commands leave the committed .gitignore alone: %s", out)
	}
}
