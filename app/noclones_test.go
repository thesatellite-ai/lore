package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCommonCommandsNeverWriteOldClones: only ~/.lore/stores holds store
// clones. In a fresh HOME with an org whose store url is canonical, the
// common commands (sync, store list/show, org refresh, migrate status,
// doctor, context --hook, where) never create ~/.lore/clones or
// ~/.lore/clones-v017, the old layouts (v0.1.17 kept a bare clone per
// store at clones/<host>/<owner>/<repo>.git). Owner item 13: a fresh one
// on a machine means another lore binary wrote it, which doctor names.
func TestCommonCommandsNeverWriteOldClones(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author().unscoped()
	home := w.env["HOME"]
	hosts := w.path("hosts")
	bare := filepath.Join(hosts, "acme", "notes.git")
	if err := os.MkdirAll(bare, 0o700); err != nil {
		t.Fatal(err)
	}
	w.git(t, bare, "init", "-q", "--bare", "-b", "main")
	gitcfg := "[user]\n\tname = Pat Example\n\temail = pat@example.com\n[init]\n\tdefaultBranch = main\n[url \"" + filepath.ToSlash(hosts) + "/\"]\n\tinsteadOf = https://github.com/\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitcfg), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := w.path("repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	w.git(t, repo, "init", "-q", "-b", "main")
	w.mustLore(t, repo, "init")
	w.mustLore(t, repo, "store", "init", "notes", "https://github.com/acme/notes.git", "--yes")
	w.mustLore(t, repo, "org", "create", "acme", "--store", "notes", "--own", "github.com/acme/*", "--key", "human:pat@example.com", "--name", "Pat Example", "--yes")
	w.mustLore(t, repo, "memory", "add", "--body", "the ledger keeps integer cents", "--to", "notes")
	if _, err := os.Stat(filepath.Join(home, ".lore", "stores", "acme", "notes", ".git")); err != nil {
		t.Fatalf("setup: the store clone is not under ~/.lore/stores: %v", err)
	}
	sweep := [][]string{
		{"sync"}, {"store", "list"}, {"store", "show"}, {"store", "show", "notes"}, {"org", "refresh"}, {"org", "refresh", "acme"},
		{"migrate", "status"}, {"doctor"}, {"context", "--hook"}, {"where"}, {"memory", "list", "--store", "notes"},
	}
	for _, dir := range []string{repo, w.path(".")} { // in the repo and outside any repo
		for _, args := range sweep {
			_, _, errOut := w.lore(t, dir, args...) // exit codes vary (doctor findings, no org outside); only the folders matter
			for _, old := range []string{"clones", "clones-v017"} {
				if _, err := os.Stat(filepath.Join(home, ".lore", old)); err == nil {
					t.Fatalf("lore %v (in %s) created ~/.lore/%s; only ~/.lore/stores may hold clones\n%s", args, dir, old, errOut)
				}
			}
		}
	}
}
