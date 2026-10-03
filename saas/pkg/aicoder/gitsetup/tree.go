package gitsetup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrCurrentBranch refuses plumbing commits onto the checked-out branch:
// moving its ref behind git's back would leave the work tree and index
// describing a different commit.
var ErrCurrentBranch = errors.New("gitsetup: refusing to commit onto the checked-out branch; edit the files and commit normally")

// ReadTree returns every file under prefix (relative to dir) as it exists in
// ref, keyed by path relative to dir — without checking anything out.
// Uses one `git ls-tree` and one `git cat-file --batch`, so cost does not
// grow with process spawns per file.
func ReadTree(ctx context.Context, dir, ref, prefix string) (map[string][]byte, error) {
	list, err := run(ctx, dir, "ls-tree", "-r", "-z", "--name-only", ref, "--", prefix)
	if err != nil {
		return nil, err
	}
	var paths []string
	for p := range strings.SplitSeq(list, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	out := map[string][]byte{}
	if len(paths) == 0 {
		return out, nil
	}
	if _, err := exec.LookPath(gitBinary); err != nil {
		return nil, ErrNoGit
	}
	cmd := exec.CommandContext(ctx, gitBinary, "cat-file", "--batch")
	cmd.Dir = dir
	var in bytes.Buffer
	for _, p := range paths {
		in.WriteString(ref + ":./" + p + "\n")
	}
	cmd.Stdin = &in
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := bufio.NewReader(stdout)
	for _, p := range paths {
		header, err := r.ReadString('\n')
		if err != nil {
			_ = cmd.Wait() // the read error is the one to report
			return nil, fmt.Errorf("gitsetup: cat-file header for %s: %w", p, err)
		}
		fields := strings.Fields(header)
		if len(fields) != catFileHeaderFields {
			_ = cmd.Wait()
			return nil, fmt.Errorf("gitsetup: cat-file %s: %s", p, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("gitsetup: cat-file size: %w", err)
		}
		buf := make([]byte, size+1) // content + trailing LF
		if _, err := io.ReadFull(r, buf); err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("gitsetup: cat-file body %s: %w", p, err)
		}
		out[p] = buf[:size]
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("gitsetup: cat-file: %w", err)
	}
	return out, nil
}

// CommitFiles writes files (paths relative to dir) into a NEW commit on
// branch, without touching the work tree, the index, or HEAD: a temporary
// index is built from the branch tip, the blobs are added, and the branch
// ref is moved with a compare-and-swap (`update-ref <new> <old>`), so a
// concurrent change to the branch makes it fail instead of losing commits.
// Nothing is pushed. Returns the new commit id.
func CommitFiles(ctx context.Context, dir, branch, message string, files map[string][]byte) (string, error) {
	if len(files) == 0 {
		return "", fmt.Errorf("gitsetup: nothing to commit")
	}
	if cur, err := run(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil && cur == branch {
		return "", ErrCurrentBranch
	}
	ref := "refs/heads/" + branch
	old, err := run(ctx, dir, "rev-parse", "--verify", ref)
	if err != nil {
		return "", fmt.Errorf("gitsetup: branch %s not found: %w", branch, err)
	}
	top, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	idx, err := os.CreateTemp("", "gitsetup-index-")
	if err != nil {
		return "", err
	}
	idxPath := idx.Name()
	_ = idx.Close()                           // git writes it; we only needed a unique path
	_ = os.Remove(idxPath)                    // read-tree refuses an empty non-index file
	defer func() { _ = os.Remove(idxPath) }() // temp file cleanup
	env := []string{"GIT_INDEX_FILE=" + idxPath}
	if _, err := runEnv(ctx, dir, env, nil, "read-tree", old); err != nil {
		return "", err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for p, content := range files {
		blob, err := runEnv(ctx, dir, env, content, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(top, filepath.Join(absDir, p))
		if err != nil {
			return "", err
		}
		if _, err := runEnv(ctx, dir, env, nil, "update-index", "--add", "--cacheinfo", gitRegularFileMode+","+blob+","+filepath.ToSlash(rel)); err != nil {
			return "", err
		}
	}
	tree, err := runEnv(ctx, dir, env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	commit, err := runEnv(ctx, dir, env, nil, "commit-tree", tree, "-p", old, "-m", message)
	if err != nil {
		return "", err
	}
	if _, err := run(ctx, dir, "update-ref", ref, commit, old); err != nil {
		return "", err
	}
	return commit, nil
}

// runEnv is run with extra environment and optional stdin.
func runEnv(ctx context.Context, dir string, env []string, stdin []byte, args ...string) (string, error) {
	if _, err := exec.LookPath(gitBinary); err != nil {
		return "", ErrNoGit
	}
	cmd := exec.CommandContext(ctx, gitBinary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// catFileHeaderFields is the field count of a `git cat-file --batch` header
// line: "<object> <type> <size>".
const catFileHeaderFields = 3

// gitRegularFileMode is git's index mode for a non-executable regular file.
const gitRegularFileMode = "100644"
