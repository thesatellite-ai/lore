package main

// later.md 618 (b11-cli port of v0.1.17 init_gitignore_test.go
// TestEnsureGitignoreHidesBrokenDBCopies): after `lore init` on the
// release binary, git itself ignores lore's machine-local files (the repo
// cache, v0.1.17's lore.db and its WAL files, the lore.db.broken.<stamp>
// copies a repair keeps, the state and backups folders, the lock) and
// still tracks the shared rows and the .gitignore itself.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitIgnored reports whether git ignores rel in dir (git check-ignore: 0
// ignored, 1 not ignored, anything else an error).
func gitIgnored(t *testing.T, dir, rel string) bool {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required: %v", err)
	}
	var stderr bytes.Buffer
	cmd := &exec.Cmd{Path: git, Args: []string{git, "-C", dir, "check-ignore", "-q", "--no-index", rel}, Stderr: &stderr}
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return true
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return false
	default:
		t.Fatalf("git check-ignore %s: %v %s", rel, err, stderr.String())
		return false
	}
}

func TestInitGitignoreHidesLocalFiles(t *testing.T) {
	asPerson(t)
	root := envScopeRepo(t) // runs `lore init`
	ignored := []string{
		".lore/lore-cache.db", ".lore/lore-cache.db-shm", ".lore/lore-cache.db-wal",
		".lore/lore.db", ".lore/lore.db-shm", ".lore/lore.db-wal",
		".lore/lore.db.broken.20261105T120000Z", ".lore/lore.db.broken.1",
		".lore/state/held.json", ".lore/backups/cache-1.db", ".lore/lock",
	}
	tracked := []string{".lore/data/_meta.json", ".lore/data/projects/x.json", ".lore/lore.dbx", ".gitignore"}
	for _, rel := range append(append([]string{}, ignored...), tracked...) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, rel := range ignored {
		if !gitIgnored(t, root, rel) {
			t.Errorf("%s is not ignored after lore init", rel)
		}
	}
	for _, rel := range tracked {
		if gitIgnored(t, root, rel) {
			t.Errorf("%s is ignored after lore init (it must stay tracked)", rel)
		}
	}
}
