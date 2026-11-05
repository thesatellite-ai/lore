package host

// gitignore.go — the first core command of a process makes sure the
// project's .gitignore lists lore's local files (STORE_SERVER_SPEC 7.4:
// "the first core command adds the same lines to an older repo's
// .gitignore, so the repo cache and lock never show in git status"); moved
// here from the old binary's ensureCoreGitignore.
//
//	Before (first op of the process, not --read-only / LORE_READ_ONLY)
//	  ─► project root: nearest folder holding .lore/ from the working
//	     folder up to the git top (none: not a lore project, nothing done)
//	  ─► each line of GitignoreLines that is neither in .gitignore nor
//	     already ignored by git (git check-ignore) is appended under "# lore"
//
// The git wiring v0.1.17 did at the same point (merge driver,
// .gitattributes, the commit/push hooks) is not here: its hooks call the
// sync commands, which move with `lore sync` (P3-HOOKS-2).

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
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

// gitignoreName is the file the lines go to, at the project root.
const gitignoreName = ".gitignore"

// gitignoreHeader introduces the appended block.
const gitignoreHeader = "\n# lore\n"

// gitignoreMode is the mode of a .gitignore lore creates (P2-LINT-1: files
// lore creates are owner-only; an existing file keeps its mode).
const gitignoreMode = 0o600

// GitignoreHook appends the missing GitignoreLines once per process, on
// the first operation that is not read-only. workDir is the folder the
// command runs in.
func GitignoreHook(workDir string) Hook {
	var once sync.Once
	return Hook{Name: HookGitignore, Before: func(ctx context.Context, run *Run) error {
		if run.ReadOnly {
			return nil // --read-only writes nothing, not even .gitignore (v0.1.17)
		}
		var err error
		once.Do(func() {
			root := projectRoot(ctx, workDir)
			if root == "" {
				return
			}
			if e := EnsureGitignore(ctx, root); e != nil {
				err = errs.Wrap(errs.Internal, "update "+gitignoreName, e)
			}
		})
		return err
	}}
}

// EnsureGitignore appends to root/.gitignore every GitignoreLines entry it
// does not list and git does not already ignore. Nothing missing: the file
// is not touched.
func EnsureGitignore(ctx context.Context, root string) (err error) {
	gi := filepath.Join(root, gitignoreName)
	existing := ""
	if b, rerr := os.ReadFile(filepath.Clean(gi)); rerr == nil {
		existing = string(b)
	}
	var add []string
	for _, line := range GitignoreLines {
		if strings.Contains(existing, line) || gitIgnores(ctx, root, line) {
			continue
		}
		add = append(add, line)
	}
	if len(add) == 0 {
		return nil
	}
	f, err := os.OpenFile(filepath.Clean(gi), os.O_CREATE|os.O_WRONLY|os.O_APPEND, gitignoreMode)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	var b strings.Builder
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(gitignoreHeader)
	for _, line := range add {
		b.WriteString(line + "\n")
	}
	_, err = f.WriteString(b.String())
	return err
}

// gitIgnores reports whether git already ignores path in root (a folder
// pattern is probed with a path inside it). Any git failure is "no".
func gitIgnores(ctx context.Context, root, path string) bool {
	probe := strings.TrimSuffix(path, "/")
	if strings.HasSuffix(path, "/") {
		probe += "/x" // check-ignore needs a path inside an ignored folder
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-q", "--", probe)
	return cmd.Run() == nil
}

// projectRoot is the lore project dir works in: the nearest folder at or
// above dir that holds .lore/, never above the git top; "" when there is
// none (not a git checkout, or no lore project on the way up).
func projectRoot(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	top := filepath.Clean(strings.TrimSpace(string(out)))
	if real, rerr := filepath.EvalSymlinks(top); rerr == nil {
		top = real
	}
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if real, rerr := filepath.EvalSymlinks(d); rerr == nil {
		d = real
	}
	for {
		if fi, serr := os.Stat(filepath.Join(d, rowfs.LoreDir)); serr == nil && fi.IsDir() {
			return d
		}
		if d == top || filepath.Dir(d) == d {
			return ""
		}
		d = filepath.Dir(d)
	}
}
