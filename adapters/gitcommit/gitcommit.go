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
	out, err := run(ctx, dir, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Describe returns a commit's subject, author ("name <email>") and
// commit time (strict ISO 8601).
func (Resolver) Describe(ctx context.Context, dir, sha string) (subject, author, committedAt string, err error) {
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

// run runs one git command (in dir when set) and returns its stdout.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, gitProgram, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gitcommit: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
