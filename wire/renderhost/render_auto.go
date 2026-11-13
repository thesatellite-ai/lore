package renderhost

// render_auto.go — when an automatic render (the post-checkout, post-merge
// and post-rewrite hooks, `lore sync`) may write .lore/LORE.md and
// LORE.full.md (A-12, B-8, B-11):
//
//	render file tracked by git and the same bytes as the index?
//	  ├─ yes ─▶ held: the committed render belongs to that commit; an
//	  │         automatic render never dirties it (a dirty tracked file
//	  │         makes `git merge` abort, and `git checkout -- .lore/LORE.md`
//	  │         would be undone by the next checkout hook)
//	  └─ no  ─▶ free: not tracked (a render the repo ignores) or already
//	            changed by hand or by `lore render`: writing it changes
//	            nothing git did not already show
//
// The committed render is refreshed where a commit can carry it: the
// pre-commit hook re-renders it when the commit stages row files
// (CommitRender), and `lore render` writes it on request.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
)

// LoreFullRel is the full render file beside LoreMDRel, relative to the
// project root (slash-separated).
const LoreFullRel = ".lore/LORE.full.md"

// renderFileRels are the files a quiet render of .lore/LORE.md writes.
var renderFileRels = []string{LoreMDRel, LoreFullRel}

// trackedClean reports whether rel (slash-separated, under root) is a file
// git tracks whose work-tree bytes match the index. Any git failure (not a
// repo, no git) answers false: there is nothing committed to protect.
func trackedClean(ctx context.Context, root, rel string) bool {
	if !tracked(ctx, root, rel) {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		return false // deleted in the work tree: already a change git shows
	}
	_, err := runGit(ctx, false, "-C", root, "diff", "--quiet", "--", rel)
	return err == nil // exit 1: the work tree differs from the index
}

// RenderHeld reports whether an automatic render must leave the render
// files of the project at root alone: one of them is tracked and
// unchanged, so writing it would leave a tracked file dirty in the middle
// of the person's git flow (see the file comment).
func (h *Host) RenderHeld(ctx context.Context, root string) bool {
	for _, rel := range renderFileRels {
		if trackedClean(ctx, root, rel) {
			return true
		}
	}
	return false
}

// CommitRender is the pre-commit hook's refresh (A-12): when .lore/LORE.md
// is tracked, it is rendered again from the rows the commit carries, so the
// commit that adds rows also carries their render and a later checkout has
// nothing to re-render. It returns the render files (relative to root)
// that are tracked or new, for the hook to stage; nil when the project
// renders no tracked LORE.md or the render failed (a commit is never
// refused over its render: the failure is one stderr line).
func (h *Host) CommitRender(ctx context.Context, root string) []string {
	if !tracked(ctx, root, LoreMDRel) {
		return nil
	}
	if _, err := h.Run(ctx, Flags{OutPath: filepath.Join(root, filepath.FromSlash(LoreMDRel)), NoPointer: true, Quiet: true}); err != nil {
		_, _ = os.Stderr.WriteString("lore: pre-commit: re-render " + LoreMDRel + ": " + err.Error() + "\n") // stderr gone: nothing left to report to
		return nil
	}
	var stage []string
	for _, rel := range renderFileRels {
		if tracked(ctx, root, rel) {
			stage = append(stage, rel) // an ignored LORE.full.md stays out of the commit
		}
	}
	return stage
}

// tracked reports whether git tracks rel (slash-separated, under root).
func tracked(ctx context.Context, root, rel string) bool {
	out, err := runGit(ctx, false, "-C", root, "ls-files", "--", rel)
	return err == nil && len(bytes.TrimSpace(out)) > 0
}
