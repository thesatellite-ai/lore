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
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
	opslive "github.com/thesatellite-ai/lore/ops/live"
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

// gitignoreMode is the mode of a .gitignore lore creates: git's usual
// work-tree mode, since the file is committed and read by everyone who
// clones the repo (an existing file keeps its mode).
const gitignoreMode = 0o644

// GitignoreHook appends the missing GitignoreLines once per process, on
// the first operation that is not read-only. workDir is the folder the
// command runs in.
func GitignoreHook(workDir string) Hook {
	var once sync.Once
	return Hook{Name: HookGitignore, Before: func(ctx context.Context, run *Run) error {
		if !NeedsGitignore(run) {
			return nil
		}
		var err error
		once.Do(func() {
			root := projectRoot(ctx, workDir)
			if root == "" {
				return
			}
			if e := EnsureGitignore(ctx, root); e != nil {
				err = errs.Wrap(errs.Internal, "update "+gitignoreName, e)
				var refused *errs.Error
				if errors.As(e, &refused) && refused.Code == errs.Refused {
					err = refused // keep the refusal's code and hint
				}
			}
		})
		return err
	}}
}

// NeedsGitignore reports whether run may add the .gitignore lines: never
// under --read-only (v0.1.17 wrote nothing then), never for a store-only
// command (leavesWorkRepo), and never for a live ref
// operation (ops/live), which writes no row file; v0.1.17's `lore live …`
// left the checkout as it was (P3-LIVE-1).
func NeedsGitignore(run *Run) bool {
	if run.ReadOnly {
		return false
	}
	if run.Entry == nil {
		return true
	}
	// a live ref operation, and a command that changes only a store
	// (repoleave.go), leave the repo's .gitignore alone
	name := run.Entry.Spec().Name
	return !opslive.IsOp(name) && !leavesWorkRepo(name)
}

// EnsureGitignore appends to root/.gitignore every GitignoreLines entry it
// does not list and git does not already ignore. Nothing missing: the file
// is not touched.
func EnsureGitignore(ctx context.Context, root string) (err error) {
	// Read and append through an os.Root on the repository: a cloned
	// repository can commit .gitignore as a symlink, and a link leading
	// out of the repository is refused instead of appended to (the
	// .gitattributes wiring opens the same way). A link kept inside the
	// repository still works.
	dir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	existing := ""
	if b, rerr := dir.ReadFile(gitignoreName); rerr == nil {
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
	f, err := dir.OpenFile(gitignoreName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, gitignoreMode)
	if err != nil {
		if fi, lerr := os.Lstat(filepath.Join(root, gitignoreName)); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return errs.Wrap(errs.Refused, "refusing to append to "+gitignoreName+": it is a symbolic link out of the repository", err).
				WithHint("replace " + filepath.Join(root, gitignoreName) + " with a regular file (or a link inside the repository)")
		}
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
	_, err := gitOutput(ctx, "-C", root, "check-ignore", "-q", "--", probe)
	return err == nil
}

// gitProgram is the git executable looked up on PATH.
const gitProgram = "git"

// gitOutput runs git with args and returns its stdout. The Cmd is built
// directly rather than through exec.CommandContext (as adapters/gitcommit
// does): bin is the absolute path exec.LookPath resolved and every argument
// is a separate argv entry built here, so no shell parses them; ctx ending
// kills it.
func gitOutput(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := exec.LookPath(gitProgram)
	if err != nil {
		return nil, err
	}
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...), Env: gitenv.Clean(os.Environ())} // -C names the repo, never a hook GIT_DIR
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = cmd.Process.Kill() }) // a kill of an exited process has nothing to report
	defer stop()
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return out.Bytes(), ctx.Err()
}

// projectRoot is the lore project dir works in: the nearest folder at or
// above dir that holds .lore/, never above the git top; "" when there is
// none (not a git checkout, or no lore project on the way up).
func projectRoot(ctx context.Context, dir string) string {
	out, err := gitOutput(ctx, "-C", dir, "rev-parse", "--show-toplevel")
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
