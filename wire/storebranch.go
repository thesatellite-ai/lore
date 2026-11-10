package wire

// storebranch.go — the BRANCH column of `lore store list` for a store that
// pins no branch (opsstores.DefaultBrancher). Flow:
//
//	entry without a branch ─▶ localStoreDir (the clone worktree
//	  <home>/clones/<host>/<repo>/work, no network) ─▶ its .git file names
//	  the worktree's git dir ─▶ that HEAD's `ref: refs/heads/<b>` ─▶ <b>
//
// The git backend checks the worktree out on the remote's default branch
// when it first clones an unpinned store (clone.go addWorktree) and reads
// the same HEAD back on every later open (setup), so this is the branch
// the store's rows really come from on this machine. Anything else (a
// file:// folder, no clone yet, a detached HEAD) answers "": the column
// stays empty rather than guess.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

var _ opsstores.DefaultBrancher = (*App)(nil)

// gitdirPrefix and headRefPrefix are the two git file formats read here: a
// worktree's .git file ("gitdir: <path>") and a symbolic HEAD
// ("ref: refs/heads/<branch>").
const (
	gitdirPrefix  = "gitdir: "
	headRefPrefix = "ref: refs/heads/"
)

// CloneDefaultBranch implements opsstores.DefaultBrancher from local files.
func (a *App) CloneDefaultBranch(_ context.Context, e opsstores.Entry) string {
	dir, isGit, err := a.localStoreDir(e)
	if err != nil || !isGit {
		return ""
	}
	return worktreeBranch(dir)
}

// worktreeBranch is the branch the git worktree at dir has checked out, ""
// when dir is no worktree or its HEAD is detached.
func worktreeBranch(dir string) string {
	gitdir := filepath.Join(dir, ".git")
	if b, err := os.ReadFile(gitdir); err == nil { // a linked worktree: .git is a file
		p, ok := strings.CutPrefix(strings.TrimSpace(string(b)), gitdirPrefix)
		if !ok {
			return ""
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		gitdir = p
	}
	head, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return ""
	}
	b, _ := strings.CutPrefix(strings.TrimSpace(string(head)), headRefPrefix)
	if b == strings.TrimSpace(string(head)) {
		return "" // detached, or not a branch ref
	}
	return b
}
