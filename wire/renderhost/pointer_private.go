package renderhost

// pointer_private.go — devco D-500: the pointer of a project whose .lore/
// folder git ignores.
//
// Why: in such a repo `lore render` and `lore init` stitched lore's
// pointer block at the top of the tracked AGENTS.md and CLAUDE.md (and,
// through a symlinked CLAUDE.md, into another repo's file), although no
// teammate ever receives the LORE.md it points at. Now the decision
// wire.RepoMayHoldLoreFiles is asked first (a client repo is not a
// private project, devco D-512):
//
//	repo may hold lore files ─▶ the tools' files, as before (stitchPointers)
//	private project ─▶ a tool with a git-excluded local file (CLAUDE.local.md
//	   for claude_code) gets the pointer there, excluded through
//	   .git/info/exclude; any other tool's file is left as it is with a
//	   line saying how that tool gets the notes (`lore context`, the agent
//	   hooks' session start); the git wiring says once why nothing went
//	   into the repo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/wire"
)

// stitchRepoPointers is stitchPointers for render and init: in a project
// the repo may hold lore files for, the tools' files; in a private one
// (D-500) only the tools' git-excluded local files.
func (h *Host) stitchRepoPointers(ctx context.Context, w *strings.Builder, root string, targets []contextTarget, outFile string) error {
	if wire.RepoMayHoldLoreFiles(ctx, root) {
		return h.stitchPointers(w, root, targets, outFile)
	}
	// The why (wire.PrivateProjectNotice) is said once per state by the
	// git wiring every command runs; saying it here too printed it twice
	// on init (devco D-513). Each skipped file still gets its own note.
	local, locals := h.privatePointerTargets(ctx, w, root, targets)
	if err := h.stitchPointers(w, root, local, outFile); err != nil {
		return err
	}
	for _, rel := range locals {
		if err := excludeInGitAs(ctx, root, rel, privatePointerExcludeComment); err != nil {
			return err
		}
	}
	return nil
}

// privatePointerExcludeComment is the .git/info/exclude line above the
// local file holding the pointer of a private project.
const privatePointerExcludeComment = "# lore: pointer to the private project's .lore/LORE.md (never commit)"

// privatePointerTargets maps targets onto their tools' git-excluded local
// files (toolprofile Profile.PrivateFile), once per file; a target whose
// tool has none, or whose local file lore must not write (a symlink, a
// tracked file: checkLocalFile), is left out with a note. locals are the
// local files' repo-relative paths, to exclude after the stitch.
func (h *Host) privatePointerTargets(ctx context.Context, w *strings.Builder, root string, targets []contextTarget) (out []contextTarget, locals []string) {
	seen := map[string]bool{}
	for _, t := range targets {
		if !t.profile.HasPointerFile() {
			continue
		}
		file := filepath.Join(root, filepath.FromSlash(t.file))
		if !t.profile.HasPrivateFile() {
			h.pointerSkipped(w, file, fmt.Sprintf(privateNoLocalFile, toolName(t)))
			continue
		}
		rel := t.profile.PrivateFile
		if err := checkLocalFile(ctx, root, rel); err != nil {
			h.pointerSkipped(w, file, "this project is private to this machine and "+rel+" cannot hold the pointer: "+err.Error())
			continue
		}
		locals = appendOnce(locals, seen, rel)
		out = append(out, contextTarget{profile: t.profile, file: rel, known: t.known})
	}
	return out, locals
}

// appendOnce appends s to list unless seen has it.
func appendOnce(list []string, seen map[string]bool, s string) []string {
	if seen[s] {
		return list
	}
	seen[s] = true
	return append(list, s)
}

// privateNoLocalFile is the skip note of a tool file in a private project
// whose tool has no git-excluded local file; %s is the tool.
const privateNoLocalFile = "this project is private to this machine (.lore/ is ignored by git), so lore adds no pointer to this tracked file; %s gets the notes through `lore context --for-prompt <text>` or the agent hooks (`lore setup --agent-hooks`)"
