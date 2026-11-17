package wire

// privateproject.go — devco D-500: a project whose .lore/ folder git
// ignores is private to this machine.
//
// Why: in a code repo whose .gitignore lists .lore/, lore still wrote a
// pointer block at the top of the tracked AGENTS.md and CLAUDE.md, its
// block into the tracked .gitattributes and its hook blocks into a hooks
// folder committed with the repo (core.hooksPath=.githooks). None of
// that helps: no teammate ever receives the rows those lines refer to,
// and every line is a change to the team's files the person has to undo
// by hand. One decision, asked by render, init, the pointer stitcher, the
// .gitattributes block and the hook installers:
//
//	repo may hold lore files = not a client repo AND git does not ignore .lore/
//
// When it is false lore writes only what git never commits: the files
// inside the ignored .lore/, the tool's git-excluded local file
// (CLAUDE.local.md) and .git/ (config, hooks there).

import (
	"context"
	"os"
	"path/filepath"
)

// LoreDirIgnored reports whether git ignores the .lore folder of the
// project at root (`git check-ignore` answers yes for it): the project is
// private to this machine. A root that is not in a git checkout, has no
// .lore folder, or a git that fails reads as not ignored (lore then
// behaves as before).
func LoreDirIgnored(ctx context.Context, root string) bool {
	if fi, err := os.Stat(filepath.Join(root, loreDirName)); err != nil || !fi.IsDir() {
		return false
	}
	_, err := gitRun{dir: root, args: []string{"check-ignore", "-q", "--", loreDirName}}.output(ctx)
	return err == nil // exit 0: ignored; 1: not ignored; 128: not a checkout
}

// RepoMayHoldLoreFiles is the one decision whether lore may write into
// files the repo at root tracks or would commit (pointer blocks in the
// tools' files, the .gitattributes block, hook blocks in a hooks folder
// committed with the repo): never in a client repo (client), never in a
// project whose .lore folder git ignores (LoreDirIgnored, devco D-500).
func RepoMayHoldLoreFiles(ctx context.Context, root string, client bool) bool {
	return !client && !LoreDirIgnored(ctx, root)
}

// PrivateProjectLine says once why lore wrote nothing into the repo's
// tracked files; %s is the project root.
const PrivateProjectLine = "%s keeps .lore/ out of git (.gitignore ignores it), so this project is private to this machine: lore writes no pointer block, no .gitattributes lines and no hook block into the repo's tracked files; your agent reads the notes through its git-excluded local file (CLAUDE.local.md for Claude Code) or `lore context`, and nothing reaches teammates"
