package app

// leaksnohead_test.go — owner item 45 (plain files round, 2026-11-12):
// `lore check leaks --staged` (the commit hook) failed E_LEAK_BLOCKED
// "the scanner could not be built", with no cause, in a repo lore is not
// set up in (no .lore; here a brand-new repo with no commit yet), so a
// person could not make the repo's first commit. The scanner builds from
// the built-in rules there, a clean commit passes, a secret is still
// refused, and a scanner that truly cannot be built names its cause.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLeaksStagedNoHeadNoLore(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author().unscoped()
	repo := w.path("fresh")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	w.git(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(t, repo, "add", "a.txt")
	if code, out, errOut := w.lore(t, repo, "check", "leaks", "--staged"); code != 0 {
		t.Fatalf("clean first commit refused: exit %d\n%s\n%s", code, out, errOut)
	}
	// A secret in the first commit is still refused (built at run time so
	// this file holds no credential-shaped text).
	key := "sk-" + "proj-" + strings.Repeat("Ab3", 16)
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("key = \""+key+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(t, repo, "add", "b.txt")
	code, out, errOut := w.lore(t, repo, "check", "leaks", "--staged")
	if code == 0 || !strings.Contains(out+errOut, "E_LEAK_BLOCKED") || !strings.Contains(out+errOut, "b.txt:1") {
		t.Fatalf("secret in the first commit not refused: exit %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out+errOut, "could not be built") {
		t.Fatalf("scanner build failure instead of a finding:\n%s\n%s", out, errOut)
	}
}
