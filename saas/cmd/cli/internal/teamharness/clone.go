package teamharness

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Clone is one checkout of a code repo by one actor. Its git commands run with
// the actor's environment, so lore's git hooks (pre-commit, post-commit,
// pre-push…) run as that actor, with the lore binary on PATH.
type Clone struct {
	A      *Actor
	Dir    string
	Remote *Remote
	extra  []string // extra env for this view only (With)
}

// With returns a view of the clone whose commands get extra env entries
// ("KEY=value"), e.g. crash injection or LORE_LIVE_AUTOPUSH=0.
func (c *Clone) With(env ...string) *Clone {
	cp := *c
	cp.extra = append(append([]string{}, c.extra...), env...)
	return &cp
}

// In returns a view of the clone rooted at a subfolder (a monorepo project).
func (c *Clone) In(sub string) *Clone {
	cp := *c
	cp.Dir = filepath.Join(c.Dir, sub)
	mustMkdir(c.A.w.T, cp.Dir)
	return &cp
}

// As returns the same checkout driven by another actor session that shares
// the machine (an agent working in its owner's checkout).
func (c *Clone) As(a *Actor) *Clone {
	cp := *c
	cp.A = a
	return &cp
}

// Lore runs lore in the clone and returns what the user saw.
func (c *Clone) Lore(args ...string) Result {
	c.A.w.T.Helper()
	return c.A.w.run(c.Dir, c.A.environ(c.extra), c.A.w.Bin, args...)
}

// MustLore runs lore and fails the test on a non-zero exit.
func (c *Clone) MustLore(args ...string) Result {
	c.A.w.T.Helper()
	return c.Lore(args...).OK(c.A.w.T)
}

// Git runs real git in the clone as the actor and fails on a non-zero exit.
func (c *Clone) Git(args ...string) string {
	c.A.w.T.Helper()
	r := c.TryGit(args...)
	if r.Code != 0 {
		c.A.w.T.Fatalf("git %v in %s: exit %d\n%s%s", args, c.Dir, r.Code, r.Stdout, r.Stderr)
	}
	return strings.TrimSpace(r.Stdout)
}

// TryGit runs real git in the clone and returns the result.
func (c *Clone) TryGit(args ...string) Result {
	c.A.w.T.Helper()
	return c.A.w.run(c.Dir, c.A.environ(c.extra), c.A.w.realGit, args...)
}

// AdminPush pushes past the protected-main hook (the merge button).
func (c *Clone) AdminPush(args ...string) {
	c.A.w.T.Helper()
	c.With(EnvAdminPush + "=1").Git(append([]string{"push", "-q"}, args...)...)
}

// CommitAll stages everything and commits (hooks run).
func (c *Clone) CommitAll(msg string) {
	c.A.w.T.Helper()
	c.Git("add", "-A")
	c.Git("commit", "-q", "--allow-empty", "-m", msg)
}

// WriteFile writes a file relative to the clone.
func (c *Clone) WriteFile(rel, body string) {
	writeFile(c.A.w.T, filepath.Join(c.Dir, rel), body)
}

// ReadFile reads a file relative to the clone; ok is false when absent.
func (c *Clone) ReadFile(rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(c.Dir, rel)))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		c.A.w.T.Fatal(err)
	}
	return string(b), true
}

// Head is the current commit.
func (c *Clone) Head() string { return c.Git("rev-parse", "HEAD") }

// LocalRef returns what a local ref points at, or "".
func (c *Clone) LocalRef(ref string) string {
	r := c.TryGit("rev-parse", "--verify", "-q", ref)
	if r.Code != 0 {
		return ""
	}
	return strings.TrimSpace(r.Stdout)
}

// Status is `git status --porcelain` (empty = clean).
func (c *Clone) Status() string { return c.Git("status", "--porcelain") }

// Pending is a lore command prepared but not started, for Simultaneously.
type Pending struct {
	cmd     *exec.Cmd
	out     bytes.Buffer
	errb    bytes.Buffer
	args    []string
	started error
}

// Prepare builds a lore command to start later at the same instant as others.
func (c *Clone) Prepare(args ...string) *Pending {
	p := &Pending{args: args}
	p.cmd = exec.Command(c.A.w.Bin, args...)
	p.cmd.Dir = c.Dir
	p.cmd.Env = c.A.environ(c.extra)
	p.cmd.Stdout, p.cmd.Stderr = &p.out, &p.errb
	return p
}

// Command builds a lore command in the clone with the actor's environment,
// for a long-running process the test drives itself (`lore api`). The
// caller starts it and must stop it (Process.Kill in t.Cleanup).
func (c *Clone) Command(args ...string) *exec.Cmd {
	cmd := exec.Command(c.A.w.Bin, args...)
	cmd.Dir = c.Dir
	cmd.Env = c.A.environ(c.extra)
	return cmd
}
