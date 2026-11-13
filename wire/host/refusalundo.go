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
	"errors"
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
	_, _ = fmt.Fprintf(w, "lore: the command was refused, so what it changed first is undone (%s); the next write that goes through sets it up again\n", strings.Join(undone, ", "))
}

// treeSnapshot is the work tree of a repo with unmigrated lore 0.1.17 rows
// before a write (W-6): a person's first write there runs lore fix's safe
// steps (migrate, render) before the write itself, and when the write is
// then refused those rewritten files must go back too. It records the bytes
// of every tracked file already changed and of every untracked file;
// restore puts back only the paths that changed since, so a person's own
// edit the refused write never touched stays exactly as it was (its bytes
// and whether it is staged).
type treeSnapshot struct {
	top       string
	dirty     map[string]fileState // tracked paths changed before the write
	untracked map[string][]byte    // untracked files before the write
}

// fileState is one path's bytes before the write (gone: it was deleted).
type fileState struct {
	body []byte
	gone bool
}

// gitIn runs git in dir with lore's clean environment.
func gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitenv.Clean(os.Environ())
	out, err := cmd.Output()
	return string(out), err
}

// statusPaths is `git status --porcelain -z -uall` as tracked-changed and
// untracked path sets (paths relative to the top).
func statusPaths(ctx context.Context, top string) (tracked, untracked map[string]bool, err error) {
	out, err := gitIn(ctx, top, "status", "--porcelain=v1", "-z", "-uall", "--no-renames")
	if err != nil {
		return nil, nil, err
	}
	tracked, untracked = map[string]bool{}, map[string]bool{}
	for _, e := range strings.Split(out, "\x00") {
		if len(e) < 4 {
			continue
		}
		if e[:2] == "??" {
			untracked[e[3:]] = true
		} else {
			tracked[e[3:]] = true
		}
	}
	return tracked, untracked, nil
}

// takeTreeSnapshot snapshots the work tree of the repo dir is in; nil
// outside a repo or when git cannot say.
func takeTreeSnapshot(ctx context.Context, dir string) *treeSnapshot {
	topOut, err := gitIn(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil
	}
	top := strings.TrimSpace(topOut)
	tracked, untracked, err := statusPaths(ctx, top)
	if err != nil {
		return nil
	}
	s := &treeSnapshot{top: top, dirty: map[string]fileState{}, untracked: map[string][]byte{}}
	for p := range tracked {
		b, rerr := os.ReadFile(filepath.Join(top, filepath.FromSlash(p)))
		switch {
		case rerr == nil:
			s.dirty[p] = fileState{body: b}
		case errors.Is(rerr, fs.ErrNotExist):
			s.dirty[p] = fileState{gone: true}
		default:
			return nil // a file lore cannot read back: never guess
		}
	}
	for p := range untracked {
		b, rerr := os.ReadFile(filepath.Join(top, filepath.FromSlash(p)))
		if rerr != nil {
			return nil // a file lore cannot read back: never guess
		}
		s.untracked[p] = b
	}
	return s
}

// restore puts back every path that changed since the snapshot and
// returns them: a tracked file lore changed goes back to HEAD, a file the
// person had changed gets its bytes back only when they differ now (it is
// never re-staged or rewritten when untouched), a new untracked file is
// removed, an untracked file that existed gets its bytes.
func (s *treeSnapshot) restore(ctx context.Context) []string {
	if s == nil {
		return nil
	}
	tracked, untracked, err := statusPaths(ctx, s.top)
	if err != nil {
		return nil
	}
	var undone []string
	for p := range tracked {
		if _, was := s.dirty[p]; was {
			continue // compared with its bytes below
		}
		if _, cerr := gitIn(ctx, s.top, "checkout", "HEAD", "--", p); cerr == nil {
			undone = append(undone, p)
		}
	}
	for p, st := range s.dirty {
		full := filepath.Join(s.top, filepath.FromSlash(p))
		cur, rerr := os.ReadFile(full)
		switch {
		case st.gone && errors.Is(rerr, fs.ErrNotExist):
		case st.gone:
			if os.Remove(full) == nil {
				undone = append(undone, p)
			}
		case rerr == nil && string(cur) == string(st.body):
		default:
			if os.MkdirAll(filepath.Dir(full), 0o755) == nil && os.WriteFile(full, st.body, 0o644) == nil {
				undone = append(undone, p)
			}
		}
	}
	for p := range untracked {
		full := filepath.Join(s.top, filepath.FromSlash(p))
		if b, was := s.untracked[p]; was {
			if cur, rerr := os.ReadFile(full); rerr == nil && string(cur) != string(b) {
				if os.WriteFile(full, b, 0o644) == nil {
					undone = append(undone, p)
				}
			}
			continue
		}
		if os.Remove(full) == nil {
			undone = append(undone, p)
		}
	}
	sort.Strings(undone)
	return undone
}
