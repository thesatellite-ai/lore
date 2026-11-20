//go:build teame2e

package teame2e

// B11 port of live_readthrough_test.go (fixbatch A) to the team suite, so
// it runs against cmd/lore (LORE_TEAM_BIN): the default-branch read-through
// of a row this checkout does not hold (wire/host live_landed.go).

import (
	"strings"
	"testing"
)

// b11MemIDRe finds a memory id in command output.
var b11MemIDRe = idPrefixed("mem")

// A memory that is on origin/main but whose file this checkout deleted (git
// rm, not committed) is not read through (E_NOT_FOUND, not E_STALE). Once
// that delete is committed it still reads as removed (devco D-713: the
// merge base holds the file, so this checkout took it out on purpose;
// reading origin's copy through brought it back and doubled moved rows),
// and the not-found names the commit and the git step that restores it
// (devco D-777: this test still held the rule before D-713). That printed
// step works as printed: after it and a commit, an edit writes the row.
func TestB11_ReadThroughRowEditAndLocalDelete(t *testing.T) {
	rt := newRepoTeam(t)
	c := rt.c1
	id := b11MemIDRe.FindString(c.MustLore("memory", "add", "--body", "keep me around").Stdout)
	if id == "" {
		t.Fatal("no memory id")
	}
	c.CommitAll("m")
	c.Git("push", "-q", "origin", "main")
	c.Git("fetch", "-q", "origin")
	rel := ".lore/data/memories/" + id + ".json"
	c.Git("rm", "-q", rel)
	if r := c.Lore("memory", "archive", id); r.Code != 27 || !strings.Contains(r.Stderr, "E_NOT_FOUND") {
		t.Fatalf("a row deleted in this checkout is not found: %+v", r)
	}
	// devco:D-388: since S-8 a commit that drops a row file by hand is
	// refused naming the row; this test is about a drop made on purpose
	// (still on origin/main, read through), so it takes the escape.
	if r := c.TryGit("commit", "-q", "-m", "rm"); r.Code == 0 || !strings.Contains(r.Stderr+r.Stdout, "LORE_ALLOW_ROW_DROP=1") {
		t.Fatalf("a hand drop must be refused naming the escape: %+v", r)
	}
	c.With("LORE_ALLOW_ROW_DROP=1").Git("commit", "-q", "-m", "rm")
	r := c.Lore("memory", "archive", id)
	if r.Code != 27 || !strings.Contains(r.Stderr, "E_NOT_FOUND") || strings.Contains(r.Stderr, "E_STALE") {
		t.Fatalf("a delete committed here reads as removed (D-713), never E_STALE: %+v", r)
	}
	if _, ok := c.ReadFile(rel); ok {
		t.Fatalf("a refused edit materialises nothing: %s", rel)
	}
	restore := b11GitStep(t, r.Stderr)
	c.Git(restore...)
	c.Git("commit", "-q", "-m", "restore")
	if r := c.Lore("memory", "archive", id); r.Code != 0 {
		t.Fatalf("after the printed restore step an edit writes: %+v", r)
	}
	if b, ok := c.ReadFile(rel); !ok || !strings.Contains(b, "keep me around") {
		t.Fatalf("the edit materialises the row: %v %s", ok, b)
	}
}

// b11GitStep is the `git …` command a not-found hint prints in backticks,
// as git's arguments (without "git"); the test fails when there is none.
func b11GitStep(t *testing.T, stderr string) []string {
	t.Helper()
	i := strings.Index(stderr, "`git ")
	if i < 0 {
		t.Fatalf("the not-found names the git step that restores the row: %s", stderr)
	}
	rest := stderr[i+len("`git "):]
	j := strings.Index(rest, "`")
	if j < 0 {
		t.Fatalf("unterminated git step in: %s", stderr)
	}
	return strings.Fields(rest[:j])
}
