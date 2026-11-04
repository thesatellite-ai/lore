package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// memIDRe finds a memory id in command output.
var memIDRe = regexp.MustCompile(`mem_[0-9a-f]{32}`)

// TestE2E_DoctorNamesResurrectedPurgedRow (DQ4): a purged row's live file
// that an old clone brings back is hidden from every read, and `lore
// doctor` names the file to delete; once the file is gone (or re-purged)
// the finding goes away. A plain archive (not purged) is never reported.
func TestE2E_DoctorNamesResurrectedPurgedRow(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("alice")
	c.lore("init", "--non-interactive", "--name=app")
	id := memIDRe.FindString(c.lore("memory", "add", "--body", "purge me after the audit window").stdout)
	if id == "" {
		t.Fatal("no memory id")
	}
	rel := filepath.Join(".lore", "data", "memories", id+".json")
	// os.Root confines every file access below to the checkout.
	repo, err := os.OpenRoot(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }() // a root handle on a temp dir: closing it loses nothing
	live, err := repo.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	c.lore("memory", "archive", id)
	if out := c.loreAny("doctor").stdout; strings.Contains(out, "purged row") {
		t.Fatalf("an archived row is not a resurrected one:\n%s", out)
	}
	c.lore("sync", "purge", "--archived-before", "2099-01-01", "--confirm")

	// An old clone's copy comes back.
	if err := repo.WriteFile(rel, live, 0o600); err != nil {
		t.Fatal(err)
	}
	if out := c.loreAny("memory", "list").stdout; strings.Contains(out, id) {
		t.Fatalf("a resurrected purged row must stay hidden: %s", out)
	}
	out := c.loreAny("doctor").stdout
	want := "store repo: " + filepath.ToSlash(rel) + " is a live copy of a purged row"
	if !strings.Contains(out, want) {
		t.Fatalf("doctor must name the file:\nwant %q\n%s", want, out)
	}

	if err := repo.Remove(rel); err != nil {
		t.Fatal(err)
	}
	if out := c.loreAny("doctor").stdout; strings.Contains(out, "purged row") {
		t.Fatalf("no finding once the file is deleted:\n%s", out)
	}
}
