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
//	repo may hold lore files = git does not ignore .lore/
//
// A client repo is not a private project (devco D-512): its .lore/ is
// committed, so its pointer stays in the tools' files as before; that it
// gets no .gitattributes block or hook rules is decided where those are
// written (wire.HoldsLoreData), not here.
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
// committed with the repo): never in a project whose .lore folder git
// ignores (LoreDirIgnored, devco D-500). It takes no client flag: D-500
// passed one from render, and a client repo's pointer then went into
// CLAUDE.local.md beside its private imports, under a line claiming its
// .gitignore ignored .lore/ (devco D-512).
func RepoMayHoldLoreFiles(ctx context.Context, root string) bool {
	return !LoreDirIgnored(ctx, root)
}

// PrivateProjectNotice is the one line that says why lore wrote nothing
// into the tracked files of the private project at root. It starts with
// what git does ("git ignores lore data", the words lore used for an
// ignored .lore/ before D-500, devco D-513) and names the rule that does
// it (rule as git check-ignore -v gives it, "<source>:<line>:<pattern>";
// "" when git could not say), so init reports the ignore and what lore
// does about it in one message, not two.
func PrivateProjectNotice(root, rule string) string {
	by := ""
	if rule != "" {
		by = " (ignored by " + rule + ")"
	}
	return "git ignores lore data in " + root + by + ", so lore treats this project as private to this machine: it writes no pointer block, no .gitattributes lines and no hook block into the repo's tracked files; your agent reads the notes through its git-excluded local file (CLAUDE.local.md for Claude Code) or `lore context`, and nothing reaches teammates (to share them, remove that rule)"
}
