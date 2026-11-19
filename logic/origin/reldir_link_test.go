package origin

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRelDirThroughLink (devco D-413): the repo root comes from git (the
// real folder) while the work dir comes from $PWD, which on macOS says
// /var for /private/var. RelDir still names the folder inside the repo,
// where a plain filepath.Rel gave a ../ path and nothing was stored.
func TestRelDirThroughLink(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "private")
	if err := os.MkdirAll(filepath.Join(real, "repo", "services", "billing"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "var")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	root := filepath.Join(real, "repo")
	if got := RelDir(root, filepath.Join(link, "repo", "services", "billing")); got != "services/billing" {
		t.Errorf("RelDir(real root, linked cwd) = %q, want services/billing", got)
	}
	if got := RelDir(filepath.Join(link, "repo"), filepath.Join(real, "repo", "services")); got != "services" {
		t.Errorf("RelDir(linked root, real cwd) = %q, want services", got)
	}
	if got := RelDir(root, filepath.Join(link, "repo")); got != "" {
		t.Errorf("RelDir at the root = %q, want \"\"", got)
	}
	if got := RelDir(root, base); got != "" {
		t.Errorf("RelDir outside = %q, want \"\"", got)
	}
}
