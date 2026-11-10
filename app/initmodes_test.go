package app

// Owner bug (setup page "Set up lore here" in a fresh repo): lore init left
// AGENTS.md and .gitignore at -rw------- and .lore at drwxr-x---, though
// every one of them is committed and read by whoever clones the repo; and
// the setup's chosen tool (Claude Code) got AGENTS.md instead of CLAUDE.md.
// Pins: what init writes in the repo is 0644 (folders 0755), and
// `lore init --target claude_code` (the setup page's fix for a setup that
// chose Claude Code) writes CLAUDE.md, not AGENTS.md.

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// committedFileMode and committedDirMode are git's usual work-tree modes.
const (
	committedFileMode fs.FileMode = 0o644
	committedDirMode  fs.FileMode = 0o755
)

func TestInitWritesCommittedFilesShareable(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person()
	root := w.initRepo(t)
	files := []string{".gitignore", "AGENTS.md", ".lore/data/_meta.json"}
	files = append(files, relAll(t, root, rowFiles(t, root, "projects"))...)
	files = append(files, relAll(t, root, rowFiles(t, root, "repos"))...)
	for _, f := range files {
		if got := modeOf(t, filepath.Join(root, f)); got != committedFileMode {
			t.Errorf("%s mode %v, want %v (committed: everyone who clones reads it)", f, got, committedFileMode)
		}
	}
	for _, d := range []string{".lore", ".lore/data", ".lore/data/projects"} {
		if got := modeOf(t, filepath.Join(root, d)); got != committedDirMode {
			t.Errorf("%s mode %v, want %v", d, got, committedDirMode)
		}
	}
}

func TestInitTargetWritesTheChosenToolsFile(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().unscoped()
	w.git(t, w.root, "init", "-q", "-b", "main")
	w.mustLore(t, w.root, "init", "--target", "claude_code")
	if got := modeOf(t, filepath.Join(w.root, "CLAUDE.md")); got != committedFileMode {
		t.Fatalf("CLAUDE.md mode %v, want %v", got, committedFileMode)
	}
	if _, err := os.Stat(filepath.Join(w.root, "AGENTS.md")); err == nil {
		t.Fatal("init --target claude_code also wrote AGENTS.md")
	}
}

// modeOf is the permission bits of path (fails the test when missing).
func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// relAll makes paths relative to root (slash form).
func relAll(t *testing.T, root string, paths []string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		r, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(r))
	}
	return out
}
