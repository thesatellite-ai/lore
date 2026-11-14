package orgcfg

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRepoTopProjectIDAsksNoGitAtTheTop: a checkout root holding .git is
// its own repo top, so finding the repo's project for a package costs no
// git run there (every plain read of an unlinked repo goes through it).
func TestRepoTopProjectIDAsksNoGitAtTheTop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for git")
	}
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + calls + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	top := t.TempDir()
	if err := os.Mkdir(filepath.Join(top, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := repoTopProjectID(context.Background(), top); got != "" {
		t.Fatalf("repoTopProjectID(top) = %q, want \"\"", got)
	}
	if b, err := os.ReadFile(calls); err == nil {
		t.Fatalf("repoTopProjectID ran git at a folder holding .git:\n%s", b)
	}

	// A folder without .git still asks git where its top is.
	pkg := filepath.Join(top, "pkg")
	if err := os.Mkdir(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = repoTopProjectID(context.Background(), pkg)
	if _, err := os.ReadFile(calls); err != nil {
		t.Fatalf("repoTopProjectID did not ask git for the top of a package folder: %v", err)
	}
}
