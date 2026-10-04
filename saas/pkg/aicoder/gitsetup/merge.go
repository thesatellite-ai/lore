package gitsetup

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Merge mechanics for tools that merge one ref into the current branch
// without a person present (a CI job, a bot). They answer three questions a
// caller needs before it may push anything: would a plain merge conflict,
// which files, and did a merge that used custom drivers leave anything
// unmerged. Policy (which files a tool may resolve, when to give up) stays
// with the caller.

// probeMinVersion is the first git release with both features ProbeMerge
// needs: `merge-tree --write-tree` (2.38, a real merge that touches nothing)
// and the global `--attr-source` option (2.40, used to switch merge
// attributes off).
var probeMinVersion = [2]int{2, 40}

// ErrGitTooOld is returned when the installed git lacks a feature a function
// needs; the message names the required version.
var ErrGitTooOld = errors.New("gitsetup: git is too old")

// MergeProbe is the outcome of testing a merge without performing it.
type MergeProbe struct {
	// UpToDate is true when theirs is already contained in HEAD: there is
	// nothing to merge.
	UpToDate bool
	// Conflicted lists the paths (relative to the repository root, slash
	// separated) that a plain merge would leave conflicted. Empty with
	// UpToDate false means the merge is clean.
	Conflicted []string
}

// ProbeMerge reports what merging theirs into HEAD would do with git's
// built-in merge only, without changing the work tree, the index or any ref.
//
// Merge attributes are switched off for the probe: merge-tree DOES run a
// custom merge driver that .gitattributes names and the clone has configured,
// so a probe run in a developer's clone would report "clean" where a host's
// web merge button (which never runs custom drivers) reports a conflict.
// Reading attributes from the empty tree (--attr-source) makes every file
// merge as plain text, which is what the host sees.
//
// Needs git 2.40 or later (ErrGitTooOld otherwise).
func ProbeMerge(ctx context.Context, dir, theirs string) (MergeProbe, error) {
	if err := requireGit(ctx, dir, probeMinVersion); err != nil {
		return MergeProbe{}, err
	}
	// The empty tree's id depends on the repository's hash (SHA-1 or
	// SHA-256), so ask this repository for it.
	emptyTree, err := run(ctx, dir, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return MergeProbe{}, err
	}
	if _, err := run(ctx, dir, "merge-base", "--is-ancestor", theirs, "HEAD"); err == nil {
		return MergeProbe{UpToDate: true}, nil
	} else if !isExitCode(err, gitExitNo) {
		return MergeProbe{}, err
	}
	out, err := runRaw(ctx, dir, "--attr-source="+emptyTree, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", "HEAD", theirs)
	if err == nil {
		return MergeProbe{}, nil
	}
	if !isExitCode(err, gitExitNo) {
		return MergeProbe{}, err
	}
	// Exit 1 = conflicts: the tree oid, then each conflicted path, all
	// NUL-terminated.
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	probe := MergeProbe{}
	seen := map[string]bool{}
	for _, p := range fields[1:] {
		if p != "" && !seen[p] {
			seen[p] = true
			probe.Conflicted = append(probe.Conflicted, p)
		}
	}
	return probe, nil
}

// MergeNoCommit merges theirs into the current branch and stops before
// committing, with config applied to this one git invocation only (`git -c
// k=v`), so merge drivers can be supplied without writing the repository's
// config. A conflicted merge is not an error: check Unmerged afterwards.
func MergeNoCommit(ctx context.Context, dir, theirs string, config [][2]string) error {
	args := make([]string, 0, 2*len(config)+5)
	for _, kv := range config {
		args = append(args, "-c", kv[0]+"="+kv[1])
	}
	args = append(args, "merge", "--no-ff", "--no-commit", "--", theirs)
	_, err := run(ctx, dir, args...)
	if err != nil && isExitCode(err, gitExitNo) {
		return nil // conflicts; the caller inspects Unmerged
	}
	return err
}

// Unmerged lists paths (repository-root relative) still conflicted in the
// index.
func Unmerged(ctx context.Context, dir string) ([]string, error) {
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

// AbortMerge undoes an in-progress merge, restoring the pre-merge state.
func AbortMerge(ctx context.Context, dir string) error {
	_, err := run(ctx, dir, "merge", "--abort")
	return err
}

// CommitMerge records the in-progress merge with message and returns the new
// commit id. Hooks are skipped (--no-verify): the caller has already
// validated the result, and a hook installed for interactive use (one that
// expects a terminal or a local tool) must not decide an unattended merge.
func CommitMerge(ctx context.Context, dir, message string) (string, error) {
	if _, err := run(ctx, dir, "commit", "--no-verify", "-m", message); err != nil {
		return "", err
	}
	return run(ctx, dir, "rev-parse", "HEAD")
}

// IsClean reports whether the work tree and index have no changes and no
// untracked files: a merge started from anything else could mix them in.
func IsClean(ctx context.Context, dir string) (bool, error) {
	out, err := runRaw(ctx, dir, "status", "--porcelain", "-z")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// requireGit fails with ErrGitTooOld when git is older than min.
func requireGit(ctx context.Context, dir string, min [2]int) error {
	out, err := run(ctx, dir, "version")
	if err != nil {
		return err
	}
	major, minor, ok := parseGitVersion(out)
	if !ok {
		return fmt.Errorf("gitsetup: cannot read git version from %q", out)
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
	parts := strings.SplitN(fields[gitVersionFields-1], ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	return major, minor, err1 == nil && err2 == nil
}

// gitVersionFields is the position (1-based) of the number in `git version`
// output: "git", "version", "<number>".
const gitVersionFields = 3

// gitExitNo is the exit status git uses for a negative answer that is not a
// failure: `merge-base --is-ancestor` (not an ancestor), `merge-tree` and
// `merge` (conflicts).
const gitExitNo = 1

// isExitCode reports whether err is git exiting with code.
func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

// DefaultBranch returns the default branch of remote as the local clone
// recorded it (refs/remotes/<remote>/HEAD, set by `git clone`), without the
// remote prefix. ok is false when the clone never recorded one (a repo that
// was `git init`-ed and pushed): callers pick their own fallback.
func DefaultBranch(ctx context.Context, dir, remote string) (string, bool) {
	out, err := run(ctx, dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil || out == "" {
		return "", false
	}
	return strings.TrimPrefix(out, remote+"/"), true
}
