package orgcfg

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// plainStoreFiles is a store tree in the plain layout (lore_store: 4) the
// way lore leaves it after `lore migrate` ran on an org store: the marker
// and README at the top, the store's own rows in _store/, project folders
// with rows directly under <folder>/<table>/, the org folder with its
// lore-org.yaml and the untouched copy migrate keeps beside it.
var plainStoreFiles = []string{
	"lore-store.yaml",
	"README.md",
	"_store/_meta.json",
	"_store/actors/act_01a1a5aca7f57659be7320c5f4b70033.json",
	"_store/machines/mch_01a1a5aca7f57659be7320c5f4b70033.json",
	"acme/_meta.json",
	"acme/tasks/tsk_01a1a5aca7f57659be7320c5f4b70033.json",
	"acme/memories/mem_01a1a5aca7f57659be7320c5f4b70033.json",
	"org/_meta.json",
	"org/lore-org.yaml",
	"org/" + OrgFileKept,
	"org/actors/act_01a1a5aca7f57659be7320c5f4b70034.json",
	"org/teams/tem_01a1a5aca7f57659be7320c5f4b70033.json",
}

// commitTree makes a git repo holding files (each with a small body) in
// one commit and returns its folder.
func commitTree(t *testing.T, files []string) string {
	t.Helper()
	dir := gitRepo(t, "", "")
	for _, p := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(p)), "{}\n")
	}
	ctx := context.Background()
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "fixture"},
	} {
		if _, err := gitRun(ctx, append([]string{"-C", dir}, args...)...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return dir
}

// TestCheckStoreTreePlainLayout (owner Mac run 2, item 32): an org store
// in the plain layout, including the lore-org.yaml.migrated copy that
// `lore migrate` committed itself, is a lore store; the same tree with a
// code file in it is still refused (F12).
func TestCheckStoreTreePlainLayout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := commitTree(t, plainStoreFiles)
	if err := checkStoreTree(ctx, dir, "HEAD", "example.com/acme/store", "main"); err != nil {
		t.Fatalf("plain layout store refused: %v", err)
	}
	code := commitTree(t, append([]string{"src/app.go"}, plainStoreFiles...))
	err := checkStoreTree(ctx, code, "HEAD", "example.com/acme/store", "main")
	if codeOf(err) != errs.NotAStore {
		t.Fatalf("a store holding src/app.go: %v, want E_NOT_A_STORE", err)
	}
}

// TestStoreAllowedKeptOrgFile: the migrate copy of lore-org.yaml is a
// lore file wherever lore-org.yaml may be (store top, org folder, the old
// layout's .lore/); other names next to it are not.
func TestStoreAllowedKeptOrgFile(t *testing.T) {
	t.Parallel()
	for _, p := range []string{OrgFileKept, "org/" + OrgFileKept, "team/admin/" + OrgFileKept, "org/.lore/" + OrgFileKept} {
		if !storeAllowed(p) {
			t.Errorf("storeAllowed(%q) = false, want a store file", p)
		}
	}
	for _, p := range []string{"org/lore-org.yaml.bak", "org/lore-org.yml", "_x/" + OrgFileKept} {
		if storeAllowed(p) {
			t.Errorf("storeAllowed(%q) = true, want refused", p)
		}
	}
}

// TestCheckStoreTreeNamesFullPaths (item 32b): the refusal names each
// refused file by its full path, never only its folder ("org/" hid which
// file lore refused), up to five, then counts the rest.
func TestCheckStoreTreeNamesFullPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	one := commitTree(t, append([]string{"org/notes.txt"}, plainStoreFiles...))
	err := checkStoreTree(ctx, one, "HEAD", "example.com/acme/store", "main")
	if codeOf(err) != errs.NotAStore || !strings.Contains(err.Error(), "org/notes.txt") {
		t.Fatalf("one stray file: %v, want E_NOT_A_STORE naming org/notes.txt", err)
	}
	stray := []string{"org/a.txt", "org/b.txt", "org/c.txt", "org/d.txt", "org/e.txt", "org/f.txt", "src/main.go"}
	many := commitTree(t, append(slices.Clone(stray), plainStoreFiles...))
	err = checkStoreTree(ctx, many, "HEAD", "example.com/acme/store", "main")
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	for _, p := range stray[:5] {
		if !strings.Contains(msg, p) {
			t.Errorf("refusal %q does not name %s", msg, p)
		}
	}
	if !strings.Contains(msg, "and 2 more") {
		t.Errorf("refusal %q does not count the 2 files past the first five", msg)
	}
	if strings.Contains(msg, "org/,") || strings.HasSuffix(strings.TrimSpace(msg), "org/)") {
		t.Errorf("refusal %q names a folder instead of files", msg)
	}
}
