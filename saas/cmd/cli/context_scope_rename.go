// context_scope_rename.go — the folder-rename hint of `lore sync`
// (CONTEXT_BUDGET 6.2: "lore sync sees a folder rename in git and prints
// the lore scope move command; it does not run it on its own"; D12: a
// scope's id is permanent, its path a label).
//
//	repo scopes rows ─▶ path gone from the checkout?
//	  ─▶ git log -1 -- <path>/, then git show -M --diff-filter=R <that commit>
//	  ─▶ new folder = the renamed file's path minus its path inside the old folder
//	  ─▶ print "lore scope move <old> <new>" (never run)
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// renameStatusPrefix starts a rename line of `git log --name-status`.
const renameStatusPrefix = "R"

// scopeRenameHints returns one hint per repo folder scope whose folder is
// gone and whose files git saw renamed into one existing folder. Read
// errors and a checkout without git give no hint (a hint is best effort;
// lint --context still reports the missing folder).
func scopeRenameHints(ctx context.Context, root string) []string {
	if coreApp == nil {
		return nil
	}
	rows, err := coreApp.List(ctx, store.SourceRepo, schema.TableScopes)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range rows {
		if r.Deleted {
			continue
		}
		old := strings.Trim(renderField(r, schema.FieldScopePath), "/")
		if old == "" || isDir(filepath.Join(root, filepath.FromSlash(old))) {
			continue
		}
		if nd := renamedTo(ctx, root, old); nd != "" {
			out = append(out, fmt.Sprintf("folder %s was renamed to %s in git; keep its rows' scope with: lore scope move %s %s", old, nd, old, nd))
		}
	}
	return out
}

// renamedTo is the folder the files of old moved to in the latest commit
// that renamed them, "" when none is found or that folder is gone too.
func renamedTo(ctx context.Context, root, old string) string {
	// A path-limited log cannot pair a rename (the new side is outside the
	// pathspec), so find the last commit that touched old/, then read that
	// whole commit with rename detection.
	sha, err := gitOutput(ctx, root, "log", "-1", "--format=%H", "--", old+"/")
	if err != nil || sha == "" {
		return ""
	}
	log, err := gitOutput(ctx, root, "show", "--format=", "--name-status", "-M", "--diff-filter=R", sha)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(log, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || !strings.HasPrefix(parts[0], renameStatusPrefix) {
			continue
		}
		rel, ok := strings.CutPrefix(parts[1], old+"/")
		if !ok {
			continue
		}
		nd, ok := strings.CutSuffix(parts[2], "/"+rel)
		if ok && nd != old && isDir(filepath.Join(root, filepath.FromSlash(nd))) {
			return nd
		}
	}
	return ""
}

// isDir reports whether path is an existing folder.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
