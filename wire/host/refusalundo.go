package host

// refusalundo.go — a refused write leaves the repo as it was (B1-1). The
// first write in a clone sets it up for lore before the operation runs: the
// .gitignore lines (gitignore.go), the merge driver in .git/config,
// .gitattributes and the hook blocks (gitwire.go), and the wiring stamp in
// .git/lore. When the operation is then refused (E_NEEDS_CONFIRM, a
// missing project, a guard), that set-up was a side effect of a command
// that did nothing. The Runner takes a snapshot of exactly those files
// before a write and puts them back as they were when the write fails, and
// says so in one line: the next write that goes through sets them up again.

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// setupFile is one file the first write's set-up may change, as it was.
type setupFile struct {
	path   string
	exists bool
	body   []byte
	mode   fs.FileMode
}

// setupSnapshot is the set-up files of one clone before a write.
type setupSnapshot struct {
	top   string
	files []setupFile
}

// setupHookNames are the hooks the set-up chains blocks into.
var setupHookNames = []string{hookPreCommit, hookCommitMsg, hookPostCheckout, hookPostMerge, hookPostRewrite, hookPostCommit, hookPrePush}

// takeSetupSnapshot reads the set-up files of the clone dir is in; nil
// outside a git work tree (nothing to protect).
func takeSetupSnapshot(ctx context.Context, dir string) *setupSnapshot {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-path", "hooks", "--git-path", "config", "--git-common-dir")
	cmd.Dir = dir
	cmd.Env = gitenv.Clean(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 4 {
		return nil
	}
	top, hooks, cfg, common := lines[0], lines[1], lines[2], lines[3]
	if hp, herr := gitConfigGet(ctx, dir, "core.hooksPath"); herr == nil && hp != "" {
		if !filepath.IsAbs(hp) {
			hp = filepath.Join(top, hp)
		}
		hooks = hp
	}
	paths := []string{filepath.Join(top, gitignoreName), filepath.Join(top, gitAttributesFile), cfg,
		filepath.Join(common, "info", "exclude"), filepath.Join(common, liveStateDirName, gitWiringStamp)}
	for _, n := range setupHookNames {
		paths = append(paths, filepath.Join(hooks, n))
	}
	s := &setupSnapshot{top: top}
	for _, p := range paths {
		f := setupFile{path: filepath.Clean(p)}
		if fi, serr := os.Lstat(f.path); serr == nil && fi.Mode().IsRegular() {
			if b, rerr := os.ReadFile(f.path); rerr == nil {
				f.exists, f.body, f.mode = true, b, fi.Mode().Perm()
			} else {
				continue // unreadable: never touched on restore
			}
		} else if serr == nil {
			continue // a link or folder of the person's: never touched
		}
		s.files = append(s.files, f)
	}
	return s
}

// gitConfigGet is one `git config --get key` in dir.
func gitConfigGet(ctx context.Context, dir, key string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "config", "--get", key)
	cmd.Dir = dir
	cmd.Env = gitenv.Clean(os.Environ())
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// restore puts every set-up file back as it was and returns the ones it
// changed (paths relative to the repo top where they are inside it).
func (s *setupSnapshot) restore() []string {
	if s == nil {
		return nil
	}
	var undone []string
	for _, f := range s.files {
		cur, err := os.ReadFile(f.path)
		switch {
		case !f.exists && err == nil:
			if os.Remove(f.path) == nil {
				undone = append(undone, s.rel(f.path))
			}
		case f.exists && (err != nil || string(cur) != string(f.body)):
			if os.WriteFile(f.path, f.body, f.mode) == nil {
				_ = os.Chmod(f.path, f.mode) // WriteFile keeps an existing file's mode
				undone = append(undone, s.rel(f.path))
			}
		}
	}
	sort.Strings(undone)
	return undone
}

// rel is p relative to the repo top when inside it.
func (s *setupSnapshot) rel(p string) string {
	if r, err := filepath.Rel(s.top, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

// announceUndo says which set-up files a refused write put back.
func announceUndo(w io.Writer, undone []string) {
	if len(undone) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "lore: the command was refused, so the git set-up it made first is undone (%s); the next write that goes through sets it up again\n", strings.Join(undone, ", "))
}
