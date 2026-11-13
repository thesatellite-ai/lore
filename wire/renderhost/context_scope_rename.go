// context_scope_rename.go — the folder scope notices (CONTEXT_BUDGET 6.2:
// "lore sync sees a folder rename in git and prints the lore scope move
// command; it does not run it on its own"; D12: a scope's id is permanent,
// its path a label; B3-5: a moved folder's rules must never vanish
// silently). The detection is wire.GoneScopes; this file turns it into the
// lines of lore sync (rename hints only), lore doctor and lore render
// (every gone scope) and the post-commit hook (scopes the commit removed).
//
//	repo scopes rows ─▶ path gone from the checkout? (wire.GoneScopes)
//	  ─▶ git saw the files renamed into one existing folder
//	  ─▶ "lore scope move <old> <new>" (lore fix runs it; nothing else does)

package renderhost

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/thesatellite-ai/lore/wire"
)

// goneScopes is wire.GoneScopes for the checkout at root; nil without an
// app or on a read error (a notice is best effort; lint --context still
// reports the missing folder).
func (h *Host) goneScopes(ctx context.Context, root string) []wire.GoneScope {
	if h.app == nil {
		return nil
	}
	gone, err := h.app.GoneScopes(ctx, root)
	if err != nil {
		return nil
	}
	return gone
}

// scopeRenameHints returns one hint per repo folder scope whose folder is
// gone and whose files git saw renamed into one existing folder (lore
// sync's report).
func (h *Host) scopeRenameHints(ctx context.Context, root string) []string {
	var out []string
	for _, g := range h.goneScopes(ctx, root) {
		if g.RenamedTo != "" {
			out = append(out, fmt.Sprintf("folder %s was renamed to %s in git; keep its rows' scope with: lore scope move --old %s --new %s", g.Path, g.RenamedTo, g.Path, g.RenamedTo))
		}
	}
	return out
}

// ScopeGoneLines is one line per repo folder scope whose folder is not in
// the checkout at root (renamed in git or not): lore doctor's context
// warnings and lore render's notices.
func (h *Host) ScopeGoneLines(ctx context.Context, root string) []string {
	var out []string
	for _, g := range h.goneScopes(ctx, root) {
		out = append(out, g.Line())
	}
	return out
}

// CommitScopeNotice is the post-commit hook's check (B3-5): when the HEAD
// commit at root deleted or renamed away files under a folder scope's
// folder and that folder is gone now, one line per such scope goes to w.
// A commit that removed nothing costs one git call and opens no store.
func (h *Host) CommitScopeNotice(ctx context.Context, root string, w io.Writer) {
	removed := wire.HeadRemovedPaths(ctx, root)
	if len(removed) == 0 {
		return
	}
	for _, g := range h.goneScopes(ctx, root) {
		for _, p := range removed {
			if strings.HasPrefix(p, g.Path+"/") {
				_, _ = fmt.Fprintln(w, "lore: "+g.Line()) // a hook notice: nothing to do when stderr is gone
				break
			}
		}
	}
}

// renamedTo is the folder the files of old moved to in the latest commit
// that renamed them, "" when none is found or that folder is gone too.
func renamedTo(ctx context.Context, root, old string) string {
	return wire.FolderRenamedTo(ctx, root, old)
}
