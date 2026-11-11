package wire

// gitignorelines.go — lore's local files and the project's .gitignore
// (STORE_SERVER_SPEC 7.4). A writing command adds the lines a project's
// .gitignore lacks (wire/host GitignoreHook → EnsureGitignore); a read
// never edits that tracked file (owner report 2026-11-11: `lore task list`
// in a v0.1.17 repo rewrote .gitignore): it keeps the files out of git
// status through the clone's own .git/info/exclude (EnsureLocalExclude)
// and `lore doctor` names what is missing and the command that adds it.
//
//	MissingGitignoreLines: a line counts as listed when .gitignore holds
//	   it or a rule git shares with the team ignores it; this clone's
//	   .git/info/exclude does not count (it reaches nobody else)

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// GitignoreLines are the lore paths every project's .gitignore lists: the
// v0.1.17 lore.db files (kept so both binaries write the same block), the
// machine-local state and backups, and the core's repo cache and lock.
var GitignoreLines = []string{
	".lore/lore.db",
	".lore/lore.db-shm",
	".lore/lore.db-wal",
	// restore/repair kept the replaced DB as lore.db.broken.<stamp>: a
	// whole database with private rows, never to be committed.
	".lore/lore.db.broken.*",
	".lore/state/",
	".lore/backups/",
	".lore/lore-cache.db",
	".lore/lore-cache.db-shm",
	".lore/lore-cache.db-wal",
	".lore/lock",
}

// gitignoreFile is the project's ignore file the lines belong in.
const gitignoreFile = ".gitignore"

// localExcludePath is git's per-clone ignore file, asked of git (a
// worktree or a moved git dir keeps it elsewhere than .git/info).
const localExcludePath = "info/exclude"

// localExcludeHeader introduces the lines EnsureLocalExclude adds.
const localExcludeHeader = "\n# lore: local files a read kept out of git status; `lore doctor --fix` adds them to .gitignore\n"

// localExcludeMode is the mode of an exclude file lore creates (git's own
// files in .git are 0644).
const localExcludeMode = 0o644

// MissingGitignoreLines lists the GitignoreLines that root's .gitignore
// does not hold and no shared ignore rule covers (see the file comment).
// Any git failure counts the line as missing (the doctor finding then
// says so; adding a line twice is harmless).
func MissingGitignoreLines(ctx context.Context, root string) []string {
	existing := ""
	// through an os.Root, as the append does: a .gitignore committed as a
	// link out of the repository is not followed
	if r, err := os.OpenRoot(root); err == nil {
		if b, rerr := r.ReadFile(gitignoreFile); rerr == nil {
			existing = string(b)
		}
		_ = r.Close() // read-only handle
	}
	var out []string
	for _, line := range GitignoreLines {
		if strings.Contains(existing, line) || sharedIgnores(ctx, root, line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// sharedIgnores reports whether a rule other than the clone's own
// info/exclude ignores path in root (a folder pattern is probed with a
// path inside it).
func sharedIgnores(ctx context.Context, root, path string) bool {
	probe := strings.TrimSuffix(path, "/")
	if strings.HasSuffix(path, "/") {
		probe += "/x" // check-ignore needs a path inside an ignored folder
	}
	out, err := gitRun{dir: root, args: []string{"check-ignore", "-v", "--", probe}}.output(ctx)
	if err != nil {
		return false // exit 1: not ignored
	}
	source, _, _ := strings.Cut(strings.TrimSpace(string(out)), ":")
	return !strings.HasSuffix(filepath.ToSlash(source), localExcludePath)
}

// EnsureLocalExclude appends to the clone's info/exclude every
// GitignoreLines entry git does not ignore yet, and returns them: a read
// keeps lore's cache and lock out of git status without editing the
// tracked .gitignore. Nothing missing: the file is not touched.
func EnsureLocalExclude(ctx context.Context, root string) ([]string, error) {
	var add []string
	for _, line := range MissingGitignoreLines(ctx, root) {
		probe := strings.TrimSuffix(line, "/")
		if strings.HasSuffix(line, "/") {
			probe += "/x"
		}
		if _, err := (gitRun{dir: root, args: []string{"check-ignore", "-q", "--", probe}}).output(ctx); err == nil {
			continue // already excluded here (an earlier read)
		}
		add = append(add, line)
	}
	if len(add) == 0 {
		return nil, nil
	}
	out, err := gitRun{dir: root, args: []string{"rev-parse", "--path-format=absolute", "--git-path", localExcludePath}}.output(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Clean(strings.TrimSpace(string(out)))
	if err = os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return nil, err
	}
	cur, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var b strings.Builder
	if len(cur) > 0 && !strings.HasSuffix(string(cur), "\n") {
		b.WriteString("\n")
	}
	b.WriteString(localExcludeHeader)
	for _, line := range add {
		b.WriteString(line + "\n")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, localExcludeMode)
	if err != nil {
		return nil, err
	}
	_, werr := f.WriteString(b.String())
	return add, errors.Join(werr, f.Close())
}
