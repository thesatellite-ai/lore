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

// noMarker is files without the store marker: a repo that is not a store
// (devco R-2: a repo with the marker stays a store whatever else it holds).
func noMarker(files []string) []string {
	return slices.DeleteFunc(slices.Clone(files), func(p string) bool { return p == storeMarkerFile })
}

// TestCheckStoreTreePlainLayout (owner Mac run 2, item 32): an org store
// in the plain layout, including the lore-org.yaml.migrated copy that
// `lore migrate` committed itself, is a lore store; a code repo (no
// marker) with the same files is still refused (F12).
func TestCheckStoreTreePlainLayout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := commitTree(t, plainStoreFiles)
	if err := checkStoreTree(ctx, dir, "HEAD", "example.com/acme/store", "main"); err != nil {
		t.Fatalf("plain layout store refused: %v", err)
	}
	code := commitTree(t, append([]string{"src/app.go"}, noMarker(plainStoreFiles)...))
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

// TestCheckStoreTreeStrayFilesKeepTheStore (devco R-2): files a member
// pushed by plain git into a store (a broken row-like file, a scratch
// folder, a symlink) never make it "not a lore store": the check passes,
// and StrayFiles names each one with the commit and author that added it.
func TestCheckStoreTreeStrayFilesKeepTheStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := commitTree(t, plainStoreFiles)
	writeFile(t, filepath.Join(dir, "scratch", "notes.txt"), "scratch\n")
	writeFile(t, filepath.Join(dir, "org", "notes.txt"), "scratch\n")
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=Kim Stray", "-c", "user.email=kim@example.invalid", "commit", "-q", "--no-verify", "-m", "scratch"},
	} {
		if _, err := gitRun(ctx, append([]string{"-C", dir}, args...)...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := checkStoreTree(ctx, dir, "HEAD", "example.com/acme/store", "main"); err != nil {
		t.Fatalf("a store with stray files is refused: %v", err)
	}
	strays := StrayFiles(ctx, dir, "HEAD")
	var got []string
	for _, s := range strays {
		got = append(got, s.String())
	}
	if len(strays) != 2 {
		t.Fatalf("StrayFiles = %v, want org/notes.txt and scratch/notes.txt", got)
	}
	for _, s := range strays {
		if s.Author != "Kim Stray" || s.Email != "kim@example.invalid" || s.Commit == "" {
			t.Errorf("stray %v does not name the commit and its author", s)
		}
	}
	if clean := StrayFiles(ctx, commitTree(t, plainStoreFiles), "HEAD"); len(clean) != 0 {
		t.Errorf("StrayFiles of a clean store = %v, want none", clean)
	}
}

// TestCheckStoreTreeNamesFullPaths (item 32b): the refusal names each
// refused file by its full path, never only its folder ("org/" hid which
// file lore refused), up to five, then counts the rest.
func TestCheckStoreTreeNamesFullPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	one := commitTree(t, append([]string{"org/notes.txt"}, noMarker(plainStoreFiles)...))
	err := checkStoreTree(ctx, one, "HEAD", "example.com/acme/store", "main")
	if codeOf(err) != errs.NotAStore || !strings.Contains(err.Error(), "org/notes.txt") {
		t.Fatalf("one stray file: %v, want E_NOT_A_STORE naming org/notes.txt", err)
	}
	stray := []string{"org/a.txt", "org/b.txt", "org/c.txt", "org/d.txt", "org/e.txt", "org/f.txt", "src/main.go"}
	many := commitTree(t, append(slices.Clone(stray), noMarker(plainStoreFiles)...))
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
