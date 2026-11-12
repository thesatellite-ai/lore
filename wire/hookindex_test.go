package wire_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire"
)

// hiRepo is a fresh git repo with one commit (README.md).
func hiRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	allowGit(t, dir, "init", "-q")
	hiWrite(t, dir, "README.md", "# demo\n")
	histCommit(t, dir, "first")
	return dir
}

func hiWrite(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hiOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...), Dir: dir}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestHookIndexRestore (D3, bug B1-20): a refusal inside the commit puts
// the index back to what the person staged, dropping what the pre-commit
// added; nothing is restored outside a commit hook, after the commit went
// through (HEAD moved), or when the index changed since.
func TestHookIndexRestore(t *testing.T) {
	ctx := context.Background()
	dir := hiRepo(t)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(dir, ".git", "index")) // what git sets for every commit hook
	hiWrite(t, dir, "README.md", "# demo\nmore\n")
	hiWrite(t, dir, ".lore/data/memories/mem_x.json", "{}\n")
	allowGit(t, dir, "add", "README.md")
	staged := hiOut(t, dir, "diff", "--cached", "--name-only")

	stage := func() {
		before := wire.HookIndexBegin(ctx, dir)
		allowGit(t, dir, "add", "-A", ".lore/data")
		wire.HookIndexStaged(ctx, dir, before)
	}
	stage()
	if !wire.HookIndexRestore(ctx, dir) {
		t.Fatal("refusal: not restored")
	}
	if got := hiOut(t, dir, "diff", "--cached", "--name-only"); got != staged {
		t.Fatalf("index after the refusal = %q, want %q", got, staged)
	}
	if wire.HookIndexRestore(ctx, dir) {
		t.Fatal("a second restore ran (the snapshot is used once)")
	}

	stage()
	hiWrite(t, dir, "later.md", "later\n")
	allowGit(t, dir, "add", "later.md") // the person staged more since
	if wire.HookIndexRestore(ctx, dir) {
		t.Fatal("restored over a later index")
	}

	stage()
	allowGit(t, dir, "-c", "user.name=Test Person", "-c", "user.email=test@example.test", "commit", "-q", "--no-verify", "-m", "went through")
	if wire.HookIndexRestore(ctx, dir) {
		t.Fatal("restored after the commit went through")
	}
}

// TestMigrationRewrites: the files still holding exactly the bytes a
// finished lore fix wrote are the pending migration; a file edited since,
// one the run never wrote, an unfinished run or no journal: none.
func TestMigrationRewrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := hiRepo(t)
	a, b, c := ".lore/data/memories/mem_a.json", ".lore/data/memories/mem_b.json", ".lore/data/memories/mem_c.json"
	hiWrite(t, dir, a, `{"_v": 2, "a": 1}`+"\n")
	hiWrite(t, dir, b, `{"_v": 2, "b": 1}`+"\n")
	allowGit(t, dir, "add", ".lore/data")
	tree := hiOut(t, dir, "write-tree", "--prefix=.lore/data/")
	allowGit(t, dir, "reset", "-q")
	journal := func(phase string) {
		hiWrite(t, dir, wire.MigrateJournalFile, `{"target": 2, "phase": "`+phase+`", "places": [{"name": "repo", "entry": "branch", "dir": "/elsewhere", "data": ".lore/data", "tree_before": "x", "tree_after": "`+tree+`"}]}`)
	}
	paths := []string{a, b, c}
	if got, _ := wire.MigrationRewrites(ctx, dir, paths); len(got) != 0 {
		t.Fatalf("no journal: %v, want none", got)
	}
	journal("running")
	if got, _ := wire.MigrationRewrites(ctx, dir, paths); len(got) != 0 {
		t.Fatalf("unfinished run: %v, want none", got)
	}
	journal("done")
	hiWrite(t, dir, b, `{"_v": 2, "b": 2}`+"\n") // edited after the run
	hiWrite(t, dir, c, `{"_v": 2}`+"\n")         // never written by the run
	got, err := wire.MigrationRewrites(ctx, dir, paths)
	if err != nil {
		t.Fatal(err)
	}
	if !got[a] || got[b] || got[c] || len(got) != 1 {
		t.Fatalf("pending = %v, want only %s", got, a)
	}
}
