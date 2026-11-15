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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/fspath"
	"github.com/thesatellite-ai/lore/wire"
)

// rowsDirRel is the project's row files folder, relative to the root.
const rowsDirRel = rowfs.LoreDir + "/" + rowfs.DataDir

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

// rowsUncommitted reports whether the row files of the project at root
// have changes git shows (a write in this clone not committed yet). The
// committed render is then already behind the work tree's rows, and the
// commit that carries them also carries their render, so an automatic
// render may refresh it (B-8 protects a clean tree only). Any git failure
// answers false.
func rowsUncommitted(ctx context.Context, root string) bool {
	out, err := runGit(ctx, false, "-C", root, "status", "--porcelain", "--untracked-files=all", "--", rowsDirRel)
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// tracked reports whether git tracks rel (slash-separated, under root).
func tracked(ctx context.Context, root, rel string) bool {
	out, err := runGit(ctx, false, "-C", root, "ls-files", "--", rel)
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// renderLocalComment is the .git/info/exclude comment above a render file
// keepRenderLocal kept out of git status.
const renderLocalComment = "# lore: render not committed yet; kept local so git pull of the commit that adds it works (lore render)"

// renderKeptLocalLine is the line keepRenderLocal prints when it kept the
// render files of a teammate's clone out of git status: what happened,
// why, and the exact command for the person who wants to commit them here.
const renderKeptLocalLine = "lore render: %s not committed in this repo yet; added to this clone's .git/info/exclude so a git pull of a teammate's commit that adds them works (to commit them from this clone: git add -f %s)\n"

// keepRenderLocal is the render side of stories A-1 (N-7): in a clone that
// did not make the project (wire.RenderOwnerHere is false), each render
// file in paths that git neither tracks nor ignores goes to the clone's
// .git/info/exclude. git overwrites an ignored untracked file on merge, so
// the owner's commit that adds the render no longer makes this clone's
// `git pull` abort ("untracked working tree files would be overwritten by
// merge"). Once the file is tracked the exclude line changes nothing. A
// quiet render (sync, a hook) keeps them local without the line.
func (h *Host) keepRenderLocal(ctx context.Context, w io.Writer, root string, quiet bool, paths ...string) error {
	if len(paths) == 0 || wire.RenderOwnerHere(ctx, root) {
		return nil
	}
	top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil // not a git checkout: nothing to pull
	}
	var kept []string
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p) // the out paths are relative to the project root
		}
		// canonical: git's top is the real folder, root may be spelled
		// through a link (macOS /var); a plain Rel read every file as
		// outside and none was kept local (devco D-413)
		rel, inside := fspath.Rel(top, p)
		if !inside {
			continue // outside this checkout (an --out elsewhere)
		}
		rel = filepath.ToSlash(rel)
		if _, serr := os.Stat(p); serr != nil || tracked(ctx, top, rel) || ignored(ctx, top, rel) {
			continue
		}
		if xerr := excludeInGitAs(ctx, top, rel, renderLocalComment); xerr != nil {
			return xerr
		}
		kept = append(kept, rel)
	}
	if len(kept) > 0 && w != nil && !quiet {
		_, _ = fmt.Fprintf(w, renderKeptLocalLine, strings.Join(kept, " and "), strings.Join(kept, " ")) // report line: the output writer has no error worth failing the render for
	}
	return nil
}

// ignored reports whether git ignores rel (slash-separated, under root).
func ignored(ctx context.Context, root, rel string) bool {
	_, err := runGit(ctx, false, "-C", root, "check-ignore", "-q", "--", rel)
	return err == nil
}
