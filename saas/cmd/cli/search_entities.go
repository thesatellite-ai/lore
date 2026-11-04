// search_entities.go — `<kind> search <q>` cobra wrappers
//
// Each entity has its own search command here. Pattern (kept identical so
// new entities are trivial to add):
//
//  1. Bind entitySearchFlags
//  2. runFTSAgainst(...) → ranked hits
//  3. ent.X.Query().Where(IDIn(...)).All(...)
//  4. Build entitySearchHit list, eager-load relations per entity
//  5. printSearchEnvelope with a one-line per-row formatter
//
// task search lives in task.go (richer eager-load)
package main

import (
	"dbent/gen/ent"
	"saas/pkg/aicoder/fts5"

	"github.com/spf13/cobra"
)

// genericSearchRunE is the shared cobra RunE body — every entity calls this
// with its own fetcher closure
func genericSearchRunE(cmd *cobra.Command, args []string, f *entitySearchFlags, entityKey, envelopeKind string, fetch func(*ent.Client, []string) ([]entitySearchHit, error), humanLine func(entitySearchHit) string) error {
	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer client.Close()
	hits, err := runFTSAgainst(cmd.Context(), client,
		&projresolveContext{ProjectID: rctx.ProjectID, RepoMount: rctx.RepoMount},
		entityKey, args[0], f)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		printSearchEnvelope(f.jsonOutput, envelopeKind, args[0], nil, humanLine)
		return nil
	}
	ids, snip, score := idOrder(hits)
	result, err := fetch(client, ids)
	if err != nil {
		return err
	}
	// Stitch snippets/scores from FTS pass; preserve FTS rank order
	byID := make(map[string]entitySearchHit, len(result))
	for _, h := range result {
		h.Snippet = snip[h.ID]
		h.Score = score[h.ID]
		byID[h.ID] = h
	}
	ordered := make([]entitySearchHit, 0, len(result))
	for _, id := range ids {
		if h, ok := byID[id]; ok {
			ordered = append(ordered, h)
		}
	}
	printSearchEnvelope(f.jsonOutput, envelopeKind, args[0], ordered, humanLine)
	return nil
}

// idOrder collects FTS-ranked IDs + per-ID snippet/score maps
func idOrder(hits []fts5.EntityHit) ([]string, map[string]string, map[string]float64) {
	ids := make([]string, len(hits))
	snip := make(map[string]string, len(hits))
	score := make(map[string]float64, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
		snip[h.ID] = h.Snippet
		score[h.ID] = h.BM25
	}
	return ids, snip, score
}

// trunc returns s truncated to n chars (with ellipsis)
func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// ── rule ─────────────────────────────────────────────────────────────────

// ── decision ─────────────────────────────────────────────────────────────

// ── hotfix ───────────────────────────────────────────────────────────────

// ── pattern ──────────────────────────────────────────────────────────────

// ── playbook ─────────────────────────────────────────────────────────────

// ── prompt ───────────────────────────────────────────────────────────────

// ── architecturenote ─────────────────────────────────────────────────────

// ── behaviour ────────────────────────────────────────────────────────────

// ── cookbookrecipe ───────────────────────────────────────────────────────

// ── incident ─────────────────────────────────────────────────────────────

// ── suggestion ───────────────────────────────────────────────────────────

// ── tastepref ────────────────────────────────────────────────────────────

// ── snapshot ─────────────────────────────────────────────────────────────

// ── comment ──────────────────────────────────────────────────────────────

// ── handoff ──────────────────────────────────────────────────────────────

// ── mission ──────────────────────────────────────────────────────────────

// ── tasklist ─────────────────────────────────────────────────────────────

// ── plan ─────────────────────────────────────────────────────────────────

// ── workflow ─────────────────────────────────────────────────────────────

// ── workspace ────────────────────────────────────────────────────────────

// ── techdoc ──────────────────────────────────────────────────────────────
