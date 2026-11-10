// Package gitref reads and writes git refs, trees and commits through git's
// plumbing, without a work tree, an index or HEAD — so the same code works on
// a normal clone, a linked worktree and a bare repository, and on any ref:
// a branch, a hidden ref under refs/lore/*, a tracking ref.
//
// Why it exists: lore keeps shared state on hidden refs (refs/lore/live,
// refs/lore/signals) and in bare store clones, and lands rows on the default
// branch by building a commit on top of a freshly fetched tracking ref. The
// older gitsetup.CommitFiles could do none of that: it hard-coded
// refs/heads/, needed an existing ref and a work tree, never deleted a file
// and committed even when nothing changed (LORE_TEAM_SPEC.md §10, C1–C3, C5,
// C6, C9).
//
// What it provides, all mechanism and no policy:
//   - Git, the runner seam every call goes through (Exec is the real one);
//   - Repo.ReadTree / ReadFile: files of any revision, root-relative paths;
//   - Repo.MakeCommit: a commit from a base plus adds and deletes, either
//     appended to the base's history or parentless (HistoryReplace), and a
//     no-op when the tree would not change;
//   - Repo.UpdateRef / DeleteRef: compare-and-swap ref moves that fail with
//     a *CASError when anything else moved the ref first;
//   - Repo.WriteRef: the three together, the shape every live, signals and
//     store write uses.
//
// Nothing here touches the network; pushing and fetching are in package
// gitremote. Retries, merges and the decision of what to write belong to the
// caller.
package gitref

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// Invocation is one run of the git program.
type Invocation struct {
	// Dir is the directory git runs in (a work tree, a subfolder of one, or
	// a bare repository).
	Dir string
	// Args are git's arguments, without the program name.
	Args []string
	// Env holds extra KEY=VALUE entries; they win over the inherited
	// environment.
	Env []string
	// Stdin is fed to git's standard input; nil means none.
	Stdin []byte
}

// Git runs the git program. It is the seam tests and tracing wrap: a
// recording Git can print every command (--trace), a failing one can inject
// errors, and Exec runs the real binary.
//
// Contract: Run returns git's standard output untouched. A non-zero exit is
// returned as a *Error carrying the exit code and standard error; a missing
// binary is ErrNoGit. Implementations must be safe for concurrent use.
type Git interface {
	Run(ctx context.Context, inv Invocation) ([]byte, error)
}

// ErrNoGit means the git program could not be found.
var ErrNoGit = errors.New("gitref: git not found on PATH")

// Error is a git command that exited non-zero (or could not start).
type Error struct {
	// Args are the arguments git was run with.
	Args []string
	// ExitCode is git's exit status; noExitCode when git never ran.
	ExitCode int
	// Stderr is git's standard error, trimmed.
	Stderr string
	// Err is the underlying error from os/exec.
	Err error
}

// noExitCode marks an Error whose process never produced an exit status
// (it could not start, or the context cancelled it before it ran).
const noExitCode = -1

// Error renders the command and git's own message.
func (e *Error) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.Args, " "), e.Err, e.Stderr)
}

// Unwrap exposes the os/exec error (and through it context errors).
func (e *Error) Unwrap() error { return e.Err }

// ExitCode returns git's exit status when err is (or wraps) a *Error, and
// false otherwise.
func ExitCode(err error) (int, bool) {
	var ge *Error
	if errors.As(err, &ge) && ge.ExitCode != noExitCode {
		return ge.ExitCode, true
	}
	return 0, false
}

// Stderr returns git's standard error when err is (or wraps) a *Error.
func Stderr(err error) string {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Stderr
	}
	return ""
}

// gitBinary is the program Exec runs when Binary is empty.
const gitBinary = "git"

// fixedLocale makes git print its messages in English. Callers (gitremote
// above all) classify failures by git's message text — "stale info",
// "fetch first", "protected branch" — so a translated git would turn every
// rejection into an unknown error.
const fixedLocale = "LC_ALL=C"

// noPrompt stops git from asking for a username or password on a terminal:
// lore runs git unattended (hooks, daemons, agents), where a prompt would
// hang the command forever instead of failing it.
const noPrompt = "GIT_TERMINAL_PROMPT=0"

// Exec runs the real git program.
type Exec struct {
	// Binary is the program to run; empty means "git" from PATH.
	Binary string
}

// Run implements Git.
func (x Exec) Run(ctx context.Context, inv Invocation) ([]byte, error) {
	bin := x.Binary
	if bin == "" {
		bin = gitBinary
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, ErrNoGit
	}
	if obs := observeWrite(ctx, inv); obs != nil {
		// Before and after, as ReadCache.runWrite drops its own answers:
		// no read stored while this runs may predate it.
		obs(inv)
		defer obs(inv)
	}
	// The Cmd is built directly rather than through exec.CommandContext:
	// path is the absolute program exec.LookPath resolved, every argument
	// is its own argv entry built by this package (no shell parses them),
	// and runCancellable kills it when ctx ends, as CommandContext did.
	cmd := &exec.Cmd{Path: path, Args: append([]string{path}, inv.Args...), Dir: inv.Dir}
	cmd.Env = append(gitenv.Clean(os.Environ()), fixedLocale, noPrompt)
	cmd.Env = append(cmd.Env, inv.Env...)
	if inv.Stdin != nil {
		cmd.Stdin = bytes.NewReader(inv.Stdin)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := runCancellable(ctx, cmd); err != nil {
		code := noExitCode
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = fmt.Errorf("%w (%w)", ctxErr, err)
		}
		return out.Bytes(), &Error{Args: inv.Args, ExitCode: code, Stderr: strings.TrimSpace(stderr.String()), Err: err}
	}
	return out.Bytes(), nil
}

// runCancellable runs cmd and kills it when ctx ends, the behaviour of
// exec.CommandContext: a context that is already done refuses to start
// (its error is returned), and a kill mid-run surfaces as the process's
// own exit error, which Run wraps with ctx.Err().
func runCancellable(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// The kill only fails when the process already exited, which
			// Wait below reports; there is nothing else to do with it.
			if kerr := cmd.Process.Kill(); kerr != nil {
				return
			}
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	return err
}
