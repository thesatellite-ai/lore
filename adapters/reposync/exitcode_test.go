package reposync

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitfast"
)

// TestQuietNoIsAnExitCode: `merge-base --is-ancestor` answered "no" by the
// process's gitfast Session (no git process) is still exit 1 to
// isExitCode (devco:D-418: ci-merge failed E_INTERNAL "test merge of
// origin/main" when only *exec.ExitError counted).
func TestQuietNoIsAnExitCode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := filepath.Join(t.TempDir(), "r")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", dir},
		{"-C", dir, "-c", "user.name=A", "-c", "user.email=a@example.test", "commit", "-q", "--allow-empty", "-m", "one"},
		{"-C", dir, "branch", "old"},
		{"-C", dir, "-c", "user.name=A", "-c", "user.email=a@example.test", "commit", "-q", "--allow-empty", "-m", "two"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	s := gitfast.New()
	defer s.Close()
	ctx := gitfast.With(context.Background(), s)
	_, err := run(ctx, dir, "merge-base", "--is-ancestor", "main", "old")
	if err == nil || !isExitCode(err, 1) {
		t.Fatalf("main is not an ancestor of old: want exit 1, got %v", err)
	}
	if _, err := run(ctx, dir, "merge-base", "--is-ancestor", "old", "main"); err != nil {
		t.Fatalf("old is an ancestor of main: %v", err)
	}
}
