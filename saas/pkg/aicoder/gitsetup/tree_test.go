package gitsetup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

func TestReadTreeAndCommitFiles(t *testing.T) {
	t.Parallel()
	dir := gitInit(t)
	ctx := context.Background()
	sub := filepath.Join(dir, "proj", "d")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.json"), []byte("{\"a\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "a")
	gitIn(t, dir, "branch", "other")
	gitIn(t, dir, "checkout", "-q", "-b", "work")
	proj := filepath.Join(dir, "proj")

	got, err := ReadTree(ctx, proj, "other", "d")
	if err != nil || string(got["d/a.json"]) != "{\"a\":1}\n" {
		t.Fatalf("read tree: %v %q", err, got)
	}
	if empty, err := ReadTree(ctx, proj, "other", "missing"); err != nil || len(empty) != 0 {
		t.Fatalf("missing prefix: %v %v", empty, err)
	}

	sha, err := CommitFiles(ctx, proj, "other", "promote b", map[string][]byte{"d/b.json": []byte("{\"b\":2}\n")})
	if err != nil || sha == "" {
		t.Fatal(err)
	}
	got, err = ReadTree(ctx, proj, "other", "d")
	if err != nil || len(got) != 2 || string(got["d/b.json"]) != "{\"b\":2}\n" {
		t.Fatalf("after promote: %v %v", got, err)
	}
	// The checked-out branch and work tree are untouched.
	if _, err := os.Stat(filepath.Join(sub, "b.json")); !os.IsNotExist(err) {
		t.Fatal("work tree modified")
	}
	if st := gitIn(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("index/work tree dirty: %q", st)
	}
	if _, err := CommitFiles(ctx, proj, "work", "x", map[string][]byte{"d/c.json": []byte("{}")}); err != ErrCurrentBranch {
		t.Fatalf("current branch must be refused: %v", err)
	}
	if _, err := CommitFiles(ctx, proj, "nope", "x", map[string][]byte{"d/c.json": []byte("{}")}); err == nil {
		t.Fatal("unknown branch must fail")
	}
	if _, err := CommitFiles(ctx, proj, "other", "x", nil); err == nil {
		t.Fatal("empty commit must fail")
	}
}
