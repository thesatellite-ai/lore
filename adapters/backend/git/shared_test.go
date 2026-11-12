package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// commitOn makes a commit on top of parent in the clone at dir adding
// file name with body (plumbing only: nothing checked out changes).
func commitOn(t *testing.T, dir, parent, name, body string) string {
	t.Helper()
	blob := strings.TrimSpace(gitT(t, dir, "hash-object", "-w", "--stdin")) // empty blob unless body
	if body != "" {
		f := filepath.Join(t.TempDir(), "body")
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		blob = strings.TrimSpace(gitT(t, dir, "hash-object", "-w", f))
	}
	idx := filepath.Join(t.TempDir(), "index")
	env := "GIT_INDEX_FILE=" + idx
	run := func(args ...string) string {
		t.Helper()
		b, err := lookGit(DefaultBinary)
		if err != nil {
			t.Fatal(err)
		}
		out, err := runner{bin: b}.run(t.Context(), call{dir: dir, args: args, env: []string{env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}})
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("read-tree", parent)
	run("update-index", "--add", "--cacheinfo", "100644,"+blob+","+name)
	tree := run("write-tree")
	return run("commit-tree", tree, "-p", parent, "-m", "add "+name)
}

// TestAdvanceBranchFollowsCheckout (1.4): a commit the org-link code
// builds with plumbing advances the checked-out branch and its files in
// step (a person browsing the clone sees it), keeps a person's unrelated
// edit, and is refused (nothing moved) when a person's change is in the
// way; a branch not checked out moves its ref only.
func TestAdvanceBranchFollowsCheckout(t *testing.T) {
	remote := newRemote(t, true)
	dir := filepath.Join(t.TempDir(), "notes")
	b, err := OpenClone(t.Context(), remote, dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
	head := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD"))
	mine := filepath.Join(dir, "notes.txt")
	if err = os.WriteFile(mine, []byte("my own note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	next := commitOn(t, dir, head, "org/link.json", "{}\n")
	if err = AdvanceBranch(t.Context(), dir, testBranch, next, head); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD")); got != next {
		t.Fatalf("HEAD %s, want %s", got, next)
	}
	if _, err = os.Stat(filepath.Join(dir, "org", "link.json")); err != nil {
		t.Fatalf("the checkout did not follow the branch: %v", err)
	}
	if b, rerr := os.ReadFile(mine); rerr != nil || string(b) != "my own note\n" {
		t.Fatalf("a person's untracked file was touched: %q %v", b, rerr)
	}
	// a person's change to a file the new commit changes: refused
	if err = os.WriteFile(filepath.Join(dir, "org", "link.json"), []byte("{\"mine\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third := commitOn(t, dir, next, "org/link.json", "{\"lore\":2}\n")
	err = AdvanceBranch(t.Context(), dir, testBranch, third, next)
	if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("a person's change in the way: %v", err)
	}
	if got := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD")); got != next {
		t.Fatalf("a refused advance moved HEAD to %s", got)
	}
	// another branch: only its ref moves
	if err = AdvanceBranch(t.Context(), dir, "staging", third, ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitT(t, dir, "rev-parse", "refs/heads/staging")); got != third {
		t.Fatalf("staging %s, want %s", got, third)
	}
	if got := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD")); got != next {
		t.Fatal("moving another branch moved HEAD")
	}
}

// TestLockCloneIsTheStoreLock (1.1, 1.4): LockClone takes the same lock
// file as the backend's writes, reentrant through its ctx; a holder
// elsewhere makes a write wait and give up with E_STORE_LOCKED.
func TestLockCloneIsTheStoreLock(t *testing.T) {
	remote := newRemote(t, true)
	dir := filepath.Join(t.TempDir(), "notes")
	b, err := OpenClone(t.Context(), remote, dir, rowfs.FolderRoot, store.OpenOptions{LockTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	held, release, err := LockClone(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(LockPathOf(dir)); err != nil || LockPathOf(dir) != filepath.Join(dir, ".git", "lore", "lock") {
		t.Fatalf("lock file %s: %v", LockPathOf(dir), err)
	}
	// reentrant with the holding ctx
	_, inner, err := LockClone(held, dir)
	if err != nil {
		t.Fatalf("a nested LockClone with the holding ctx: %v", err)
	}
	inner()
	g := newGen(t)
	if err = b.Write(t.Context(), []store.Change{{Row: g.row(newID(t), "while held", hlc.HLC{})}}); !errs.Has(err, errs.StoreLocked) {
		t.Fatalf("a write while the clone is locked elsewhere: %v", err)
	}
	release()
	write(t, b, store.Change{Row: g.row(newID(t), "after release", hlc.HLC{})})
}
