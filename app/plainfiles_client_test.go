package app

// plainfiles_client_test.go — brief 3.8 (owner report 2026-11-12): in a
// repo that holds no lore data of its own (a client repo whose notes live
// in a store folder), `lore doctor --fix` offered to add lore's lines to
// the repo's .gitignore, a change in the client's code. No command,
// doctor --fix included, changes a tracked file there: lore's local files
// stay out of git status through .git/info/exclude only, and doctor does
// not suggest a .gitignore change.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// trackedView is every tracked file of dir with its content hash, and
// whether git sees a change to any of them.
func trackedView(t *testing.T, w *cliWorld, dir string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(w.git(t, dir, "diff", "--stat", "HEAD"))
	for _, f := range strings.Fields(w.git(t, dir, "ls-files")) {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		b.WriteString(hex.EncodeToString(sum[:]) + "  " + f + "\n")
	}
	return b.String()
}

func TestClientRepoFixLeavesTrackedFiles(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author().unscoped()
	dir := w.root
	w.git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("the client's service\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// the repo attaches a store folder; it keeps no rows of its own
	w.mustLore(t, dir, "store", "init", "inner", fileuri.URL(t.TempDir(), ""), "--yes")
	w.git(t, dir, "add", "-A", ".gitignore", "README.md", ".lore/stores.yaml")
	w.git(t, dir, "commit", "-q", "-m", "client code")
	if _, err := os.Stat(filepath.Join(dir, ".lore", "data")); err == nil {
		t.Fatal("setup: the client repo has a .lore/data folder")
	}
	before := trackedView(t, w, dir)
	for _, args := range [][]string{{"doctor"}, {"doctor", "--fix"}, {"memory", "list", "--store", "inner"}, {"doctor", "--fix"}} {
		code, out, errOut := w.lore(t, dir, args...)
		if code != 0 && args[0] != "doctor" {
			t.Fatalf("lore %v: exit %d\n%s\n%s", args, code, out, errOut)
		}
		if strings.Contains(out+errOut, "adds them (then commit .gitignore)") || strings.Contains(out+errOut, "to "+filepath.Join(dir, ".gitignore")) {
			t.Fatalf("lore %v offers or makes a .gitignore change in a client repo:\n%s\n%s", args, out, errOut)
		}
		if after := trackedView(t, w, dir); after != before {
			t.Fatalf("lore %v changed the client repo's files:\nbefore:\n%s\nafter:\n%s\nstderr:\n%s", args, before, after, errOut)
		}
	}
	ex, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(ex), ".lore/lore.db") || !strings.Contains(string(ex), ".lore/lock") {
		t.Fatalf(".git/info/exclude does not keep lore's local files out of git status: %q %v", ex, err)
	}
}
