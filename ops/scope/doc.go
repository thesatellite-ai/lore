// Package scope is the `lore scope` noun (CONTEXT_BUDGET 6.2, D10/D12) and
// the shared --scope / --overrides work of the knowledge nouns (rule,
// hotfix, decision, memory), layer L5.
//
//	<noun> add|edit --scope dir:<path> ─► ForWrite ─► Ensure (scp_ row, repo store)
//	                --overrides <id>   ─► CheckOverrides (Wider, logic/dirscope)
//	lore scope show <path>  ─► chain: project, repo, folders, glob rules
//	lore scope list         ─► every folder scope: entries, file, bytes
//	lore scope move a b     ─► relabel scopes rows only (ids unchanged)
//
// Render (saas/cmd/cli/render_scopes.go) turns the folder scopes into
// <dir>/LORE.md files plus each tool's pointer file.
package scope
