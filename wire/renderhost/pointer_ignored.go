package renderhost

// pointer_ignored.go — devco:S-2: a pointer file git ignores.
//
// A repo that gitignores its CLAUDE.md (or any tool's pointer file) keeps
// the pointer lore stitches there on this machine only: every teammate's
// clone has no such file, so their agents never load .lore/LORE.md and
// doctor is DEGRADED in each fresh clone. lore init and lore render say so
// plainly, with the ignore rule that does it and the two ways on (stop
// ignoring the file and commit it, or run lore render in every clone);
// doctor's tool_pointer finding says the same instead of a bare
// "fix: lore render" that keeps coming back.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// ignoredBy is the git ignore rule that ignores rel in the repo at root
// (".gitignore:2:/CLAUDE.md"), and whether git ignores it at all. A file
// git already tracks is not ignored (git check-ignore answers no for it).
func ignoredBy(ctx context.Context, root, rel string) (string, bool) {
	out, err := runGit(ctx, false, "-C", root, "check-ignore", "-v", "--", filepath.ToSlash(rel))
	if err != nil {
		return "", false
	}
	// "<source>:<line>:<pattern>\t<path>"
	rule, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	return rule, true
}

// ignoredPointerText is the shared sentence for an ignored pointer file:
// what it means for teammates and what to do, for init, render and doctor.
func ignoredPointerText(file, tool, rule string) string {
	return fmt.Sprintf("%s is ignored by git (%s), so the lore pointer in it stays on this machine: a teammate's clone has no %s and %s there does not load %s. "+
		"To give every clone the pointer, stop ignoring %s (remove that ignore line) and commit it; until then each person runs `lore render` once in their own clone",
		file, rule, file, tool, LoreMDRel, file)
}

// ignoredPointerLines prints one line per wired target whose pointer file
// git ignores (lore init and lore render, after the pointers are stitched).
func (h *Host) ignoredPointerLines(ctx context.Context, w *strings.Builder, root string, targets []contextTarget) {
	seen := map[string]bool{}
	for _, t := range targets {
		file := pointerFileOf(t)
		if file == "" || seen[file] {
			continue
		}
		seen[file] = true
		if rule, ok := ignoredBy(ctx, root, file); ok {
			fmt.Fprintf(w, "%s %s\n", h.decor.warn("!"), ignoredPointerText(file, toolName(t), rule))
		}
	}
}

// pointerFileOf is the repo-relative pointer file of t ("" for a profile
// without one, such as generic).
func pointerFileOf(t contextTarget) string {
	if t.known {
		if !t.profile.HasPointerFile() {
			return ""
		}
		return t.profile.PointerFile
	}
	return t.file
}

// toolName names t's tool for a person ("claude_code"), or "your agent"
// for a file given to --target.
func toolName(t contextTarget) string {
	if t.known {
		return t.profile.Name
	}
	return "your agent"
}
