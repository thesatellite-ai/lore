//go:build teame2e

package teame2e

// later.md 607 (b11-sync ports) on cmd/lore with two clones: the git
// wiring a teammate's first lore command installs guards her commits, and
// a broad repo ignore rule cannot hide lore's row files from the team.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
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

// TestE2E_SameRowMergeDriverAndConflict, ported to two clones: bob and
// carol edit different fields of one rule in their own clones; git's
// merge (lore's driver, installed by their first lore command) keeps both.
// The same field edited on both sides is a conflict whose markers keep
// both texts, and the pre-commit guard refuses to commit the marker text.
func TestTeD_MergeDriverAcrossClones(t *testing.T) {
	bc, carol := teDSoloPair(t)
	bc.MustLore("init", "--non-interactive", "--name=tool")
	id := mustID(t, bc.MustLore("rule", "add", "--body", "use JWT for the api", "--severity", "must"), "rul")
	bc.CommitAll("lore")
	bc.AdminPush("origin", "main")
	cc := carol.Clone(bc.Remote, "tool")
	cc.MustLore("rule", "list")

	bc.MustLore("rule", "edit", id, "--severity", "should")
	bc.CommitAll("bob: severity")
	bc.AdminPush("origin", "main")
	cc.MustLore("rule", "edit", id, "--body", "use JWT for the public api")
	cc.CommitAll("carol: body")
	if res := cc.TryGit("pull", "-q", "--no-rebase", "--no-edit"); res.Code != 0 {
		t.Fatalf("different fields of one row merge cleanly: %s", res)
	}
	cc.MustLore("rule", "show", id).Says(t, "use JWT for the public api", "should")
	cc.AdminPush("origin", "main")
	bc.Git("pull", "-q", "--no-rebase", "--no-edit")
	bc.MustLore("rule", "show", id).Says(t, "use JWT for the public api", "should")

	bc.MustLore("rule", "edit", id, "--body", "bob: JWT everywhere")
	bc.CommitAll("bob: body")
	bc.AdminPush("origin", "main")
	cc.MustLore("rule", "edit", id, "--body", "carol: JWT on the public api only")
	cc.CommitAll("carol: body again")
	if res := cc.TryGit("pull", "-q", "--no-rebase", "--no-edit"); res.Code == 0 {
		t.Fatalf("the same field edited on both sides must conflict: %s", res)
	}
	body, _ := cc.ReadFile(dataDir + "/" + tblRules + "/" + id + ".json")
	for _, want := range []string{"<<<<<<< ", "bob: JWT everywhere", "carol: JWT on the public api only", ">>>>>>> "} {
		if !strings.Contains(body, want) {
			t.Fatalf("the conflict keeps both sides with markers (%q): %s", want, body)
		}
	}
	cc.Git("add", "-A")
	if res := cc.TryGit("commit", "-qm", "markers"); res.Code == 0 {
		t.Fatalf("a row with conflict markers is refused at commit: %s", res)
	}
}
