package reposync

// git.go — the git mechanics install-action and ci-merge need (ported from
// saas/pkg/aicoder/gitsetup): find the work tree's top, test a merge
// without touching anything, merge with drivers supplied per invocation,
// and undo or record that merge. Policy (which files may be settled, when
// to give up) stays in ci.go.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// gitProgram is the git executable looked up on PATH.
const gitProgram = "git"

// gitExitNo is the exit status git uses for a negative answer that is not a
// failure: `merge-base --is-ancestor` (not an ancestor), `merge-tree` and
// `merge` (conflicts).
const gitExitNo = 1

// probeMinVersion is the first git release with both features probeMerge
// needs: `merge-tree --write-tree` (2.38) and `--attr-source` (2.40).
var probeMinVersion = [2]int{2, 40}

// gitVersionFields is the position (1-based) of the number in `git version`
// output: "git", "version", "<number>".
const gitVersionFields = 3

// ErrGitTooOld is returned when the installed git lacks a feature a
// function needs; the message names the required version.
var ErrGitTooOld = errors.New("reposync: git is too old")

// gitError is a failed git run with its stderr.
type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, strings.TrimSpace(e.stderr))
}

func (e *gitError) Unwrap() error { return e.err }

// runRaw executes git in dir with args and returns stdout as is.
func runRaw(ctx context.Context, dir string, args ...string) (string, error) {
	bin, err := exec.LookPath(gitProgram)
	if err != nil {
		return "", fmt.Errorf("reposync: git not found on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), &gitError{args: args, stderr: errb.String(), err: err}
	}
	return out.String(), nil
}

// run is runRaw with stdout trimmed.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := runRaw(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

// isExitCode reports whether err is git exiting with code.
func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

// toplevel is the work tree root containing dir.
func toplevel(ctx context.Context, dir string) (string, error) {
	return run(ctx, dir, "rev-parse", "--show-toplevel")
}

// isClean reports whether the work tree and index have no changes and no
// untracked files: a merge started from anything else could mix them in.
func isClean(ctx context.Context, dir string) (bool, error) {
	out, err := runRaw(ctx, dir, "status", "--porcelain", "-z")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// mergeProbe is the outcome of testing a merge without performing it.
type mergeProbe struct {
	// upToDate: theirs is already contained in HEAD.
	upToDate bool
	// conflicted lists the repository-relative paths a plain merge would
	// leave conflicted; empty with upToDate false means a clean merge.
	conflicted []string
}

// probeMerge reports what merging theirs into HEAD would do with git's
// built-in merge only, changing nothing. Merge attributes are read from the
// empty tree (--attr-source), so every file merges as plain text: what a
// host's web merge button, which never runs custom drivers, sees.
func probeMerge(ctx context.Context, dir, theirs string) (mergeProbe, error) {
	if err := requireGit(ctx, dir, probeMinVersion); err != nil {
		return mergeProbe{}, err
	}
	emptyTree, err := run(ctx, dir, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return mergeProbe{}, err
	}
	if _, err := run(ctx, dir, "merge-base", "--is-ancestor", theirs, "HEAD"); err == nil {
		return mergeProbe{upToDate: true}, nil
	} else if !isExitCode(err, gitExitNo) {
		return mergeProbe{}, err
	}
	out, err := runRaw(ctx, dir, "--attr-source="+emptyTree, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", "HEAD", theirs)
	if err == nil {
		return mergeProbe{}, nil
	}
	if !isExitCode(err, gitExitNo) {
		return mergeProbe{}, err
	}
	// Exit 1 = conflicts: the tree oid, then each conflicted path, all
	// NUL-terminated.
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	probe := mergeProbe{}
	seen := map[string]bool{}
	for _, p := range fields[1:] {
		if p != "" && !seen[p] {
			seen[p] = true
			probe.conflicted = append(probe.conflicted, p)
		}
	}
	return probe, nil
}

// mergeNoCommit merges theirs into the current branch and stops before
// committing, with config applied to this one git invocation only (`git -c
// k=v`). A conflicted merge is not an error: check unmerged afterwards.
func mergeNoCommit(ctx context.Context, dir, theirs string, config [][2]string) error {
	var args []string
	for _, kv := range config {
		args = append(args, "-c", kv[0]+"="+kv[1])
	}
	args = append(args, "merge", "--no-ff", "--no-commit", "--", theirs)
	_, err := run(ctx, dir, args...)
	if err != nil && isExitCode(err, gitExitNo) {
		return nil // conflicts; the caller inspects unmerged
	}
	return err
}

// unmerged lists paths (repository-root relative) still conflicted.
func unmerged(ctx context.Context, dir string) ([]string, error) {
	out, err := runRaw(ctx, dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for p := range strings.SplitSeq(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// abortMerge undoes an in-progress merge, restoring the pre-merge state.
func abortMerge(ctx context.Context, dir string) error {
	_, err := run(ctx, dir, "merge", "--abort")
	return err
}

// commitMerge records the in-progress merge and returns the new commit id.
// Hooks are skipped (--no-verify): the caller has validated the result, and
// a hook made for interactive use must not decide an unattended merge.
func commitMerge(ctx context.Context, dir, message string) (string, error) {
	if _, err := run(ctx, dir, "commit", "--no-verify", "-m", message); err != nil {
		return "", err
	}
	return run(ctx, dir, "rev-parse", "HEAD")
}

// requireGit fails with ErrGitTooOld when git is older than min.
func requireGit(ctx context.Context, dir string, min [2]int) error {
	out, err := run(ctx, dir, "version")
	if err != nil {
		return err
	}
	major, minor, ok := parseGitVersion(out)
	if !ok {
		return fmt.Errorf("reposync: cannot read git version from %q", out)
	}
	if major < min[0] || (major == min[0] && minor < min[1]) {
		return fmt.Errorf("%w: have %d.%d, need %d.%d or later", ErrGitTooOld, major, minor, min[0], min[1])
	}
	return nil
}

// parseGitVersion reads "git version 2.50.1 (Apple Git-155)" → 2, 50.
func parseGitVersion(s string) (int, int, bool) {
	fields := strings.Fields(s)
	if len(fields) < gitVersionFields {
		return 0, 0, false
	}
	major, rest, ok := strings.Cut(fields[gitVersionFields-1], ".")
	if !ok {
		return 0, 0, false
	}
	minor, _, _ := strings.Cut(rest, ".")
	maj, err1 := strconv.Atoi(major)
	mnr, err2 := strconv.Atoi(minor)
	return maj, mnr, err1 == nil && err2 == nil
}

// defaultBranch is the default branch of remote as the clone recorded it
// (refs/remotes/<remote>/HEAD), without the remote prefix; ok false when
// the clone never recorded one.
func defaultBranch(ctx context.Context, dir, remote string) (string, bool) {
	out, err := run(ctx, dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil || out == "" {
		return "", false
	}
	return strings.TrimPrefix(out, remote+"/"), true
}
