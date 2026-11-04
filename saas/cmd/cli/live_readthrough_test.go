package main

import (
	"os"
	"strings"
	"testing"
)

// TestE2E_ReadThroughRowEditAndLocalDelete (fixbatch A): a memory that is
// on origin/main but whose file this checkout deleted (git rm) is not read
// through (E_NOT_FOUND, not E_STALE); once that delete is committed, a row
// still on origin/main and not in the checkout is read through and an edit
// of it writes the file (materialised from origin) instead of E_STALE.
func TestE2E_ReadThroughRowEditAndLocalDelete(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("alice")
	c.lore("init", "--non-interactive", "--name=app")
	r := c.lore("memory", "add", "--body", "keep me around")
	id := idPattern.FindString(r.stdout)
	c.commitAll("m")
	c.git("push", "-q")
	c.git("fetch", "-q")
	c.git("rm", "-q", ".lore/data/memories/"+id+".json")
	if r := c.loreAny("memory", "archive", id); r.code != 27 || !strings.Contains(r.stderr, "E_NOT_FOUND") {
		t.Fatalf("a row deleted in this checkout is not found: %+v", r)
	}
	c.git("commit", "-q", "-m", "rm")
	if r := c.loreAny("memory", "archive", id); r.code != 0 {
		t.Fatalf("an edit of a read-through row must write, not E_STALE: %+v", r)
	}
	b, err := os.ReadFile(c.dataFile("memories", id))
	if err != nil || !strings.Contains(string(b), "keep me around") {
		t.Fatalf("the edit materialises the row: %v %s", err, b)
	}
}
