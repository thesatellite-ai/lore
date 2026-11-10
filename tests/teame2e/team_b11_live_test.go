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
// rm, not committed) is not read through (E_NOT_FOUND, not E_STALE); once
// that delete is committed, a row still on origin/main and not in the
// checkout is read through, and an edit of it writes the file (materialised
// from origin) instead of E_STALE.
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
	c.Git("commit", "-q", "-m", "rm")
	if r := c.Lore("memory", "archive", id); r.Code != 0 {
		t.Fatalf("an edit of a read-through row must write, not E_STALE: %+v", r)
	}
	if b, ok := c.ReadFile(rel); !ok || !strings.Contains(b, "keep me around") {
		t.Fatalf("the edit materialises the row: %v %s", ok, b)
	}
}
