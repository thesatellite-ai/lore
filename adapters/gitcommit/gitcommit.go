// Package gitcommit resolves git commit references for `lore link add`
// (it satisfies ops/link.CommitResolver; wire passes it in) by running the git program: `git rev-parse` for
// the sha and `git log -1` for the message, author and commit time, exactly
// as v0.1.17's link_cmds.go did.
//
// Depends on the external program git (on PATH); when it is missing every
// call fails with the exec error, which ops/link reports as an unresolvable
// reference.
package gitcommit

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// gitProgram is the git executable looked up on PATH.
const gitProgram = "git"

// logFormat prints subject, "name <email>" and the strict ISO commit time,
// one per line (v0.1.17's format).
const logFormat = "--format=%s%n%an <%ae>%n%cI"

// logParts is how many lines logFormat prints.
const logParts = 3

// Resolver runs git in the process's working directory, or in the
// repo_path a caller names. The zero value is ready to use.
type Resolver struct{}

// RevParse returns the full sha ref names in the repository at dir.
func (Resolver) RevParse(ctx context.Context, dir, ref string) (string, error) {
	if err := plainRef(ref); err != nil {
		return "", err
	}
	out, err := run(ctx, dir, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Describe returns a commit's subject, author ("name <email>") and
// commit time (strict ISO 8601).
func (Resolver) Describe(ctx context.Context, dir, sha string) (subject, author, committedAt string, err error) {
	if perr := plainRef(sha); perr != nil {
		return "", "", "", perr
	}
	out, err := run(ctx, dir, "log", "-1", logFormat, sha)
	if err != nil {
		return "", "", "", err
	}
	parts := strings.SplitN(strings.TrimSpace(out), "\n", logParts)
	if len(parts) >= 1 {
		subject = parts[0]
	}
	if len(parts) >= 2 {
		author = parts[1]
	}
	if len(parts) >= logParts {
		committedAt = parts[2]
	}
	return subject, author, committedAt, nil
}

// plainRef refuses a reference git would read as an option.
//
// Why: `git rev-parse --output=<file>` echoes the text back as if it were a
// sha, and `git log -1 … --output=<file>` then writes that file, so a
// `--commit=--output=…` value would write anywhere the user can.
func plainRef(ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") {
		return fmt.Errorf("gitcommit: %q is not a commit reference", ref)
	}
	return nil
}

// run runs one git command (in dir when set) and returns its stdout.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	bin, err := exec.LookPath(gitProgram)
	if err != nil {
		return "", fmt.Errorf("gitcommit: git is not installed: %w", err)
	}
	// The Cmd is built directly rather than through exec.CommandContext:
	// bin is the absolute path exec.LookPath resolved, every argument is a
	// separate argv entry built by this package (no shell parses them), and
	// runCancellable kills it when ctx ends.
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...)}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := runCancellable(ctx, cmd); err != nil {
		return "", fmt.Errorf("gitcommit: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// runCancellable starts cmd and kills it when ctx ends first; it returns
// ctx's error in that case, else cmd's.
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
			// A failed kill means the process already exited; Wait reports it.
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
