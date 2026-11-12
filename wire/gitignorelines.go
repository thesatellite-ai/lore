package wire

// gitignorelines.go — lore's local files and the project's .gitignore
// (STORE_SERVER_SPEC 7.4). A writing command adds the lines a project's
// .gitignore lacks (wire/host GitignoreHook → EnsureGitignore); a read
// never edits that tracked file (owner report 2026-11-11: `lore task list`
// in a v0.1.17 repo rewrote .gitignore): it keeps the files out of git
// status through the clone's own .git/info/exclude (EnsureLocalExclude;
// a read adds only lines for files that exist, EnsureLocalExcludeExisting,
// and creates none itself: the repo cache lives under the lore home) and
// `lore doctor` names what is missing and the command that adds it.
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

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
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

// gitignoreOnlyIfThere are the GitignoreLines a project's .gitignore only
// needs while the file is in the work tree (B4-7): the repo cache moved to
// the lore home, so this lore never makes .lore/lore-cache.db* (an older
// build's copy gets its line while it is there); the lock exists once a
// write ran here. Adding them to every .gitignore named files no command
// creates. .lore/lock stays on every list: each write here takes it, and
// the hook adds the lines before the write, so a line added only once the
// lock exists would leave it in git status after the first write.
var gitignoreOnlyIfThere = map[string]bool{
	".lore/lore-cache.db":     true,
	".lore/lore-cache.db-shm": true,
	".lore/lore-cache.db-wal": true,
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
// does not hold and no shared ignore rule covers (see the file comment);
// a gitignoreOnlyIfThere line only while its file is there.
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
		if gitignoreOnlyIfThere[line] && !localPathExists(root, line) {
			continue
		}
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
	return ensureLocalExclude(ctx, root, false)
}

// EnsureLocalExcludeExisting is EnsureLocalExclude for a read: only the
// lines naming a path that exists in root's work tree now (an older
// build's .lore/lore-cache.db, a lock or state/ a write left) are added.
// A read creates no file in the repo (the repo cache lives under the lore
// home, owner item 16), so a read in a repo whose .gitignore lacks lore's
// lines leaves .git/info/exclude as it was when nothing is there to hide.
func EnsureLocalExcludeExisting(ctx context.Context, root string) ([]string, error) {
	return ensureLocalExclude(ctx, root, true)
}

// localPathExists reports whether a GitignoreLines entry names something
// on disk under root (a glob entry: any match; a folder entry: the folder).
func localPathExists(root, line string) bool {
	p := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(line, "/")))
	if strings.ContainsAny(line, "*?[") {
		m, err := filepath.Glob(p)
		return err == nil && len(m) > 0
	}
	_, err := os.Lstat(p)
	return err == nil
}

// ensureLocalExclude is EnsureLocalExclude; onlyExisting skips the lines
// naming nothing on disk.
func ensureLocalExclude(ctx context.Context, root string, onlyExisting bool) ([]string, error) {
	var add []string
	for _, line := range MissingGitignoreLines(ctx, root) {
		if onlyExisting && !localPathExists(root, line) {
			continue
		}
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

// HoldsLoreData reports whether the project at root keeps rows of its own
// (its .lore/data folder, rowfs's layout). A repo without it (a client
// repo whose notes live in a store folder) gets no tracked file of its
// changed by lore: no .gitignore lines, no .gitattributes block (plain
// files sweep 3.8); lore's local files stay out of git status through
// .git/info/exclude.
func HoldsLoreData(root string) bool {
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rowfs.LayoutRepo.DataPath(rowfs.FolderRoot))))
	return err == nil && fi.IsDir()
}
