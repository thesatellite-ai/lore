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
	"fmt"
	"saas/pkg/constants"

	"dbent/gen/ent"
	entArchitectureNote "dbent/gen/ent/architecturenote"
	entBehaviour "dbent/gen/ent/behaviour"
	entCookbookRecipe "dbent/gen/ent/cookbookrecipe"
	entHandoff "dbent/gen/ent/handoff"
	entIncident "dbent/gen/ent/incident"
	entSnapshot "dbent/gen/ent/snapshot"
	entSuggestion "dbent/gen/ent/suggestion"
	entTastePref "dbent/gen/ent/tastepref"
	entTechDoc "dbent/gen/ent/techdoc"
	entWorkflow "dbent/gen/ent/workflow"
	entWorkspace "dbent/gen/ent/workspace"
	"saas/pkg/aicoder/errcodes"
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

func newArchitectureNoteSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across architecturenote title + body",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntityArchitectureNote.FTSEntity(), constants.EntityArchitectureNote.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.ArchitectureNote.Query().Where(entArchitectureNote.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch architecturenote").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"title": r.Title, "body": r.Body},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s %s", h.ID, row["title"])
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

// ── behaviour ────────────────────────────────────────────────────────────

func newBehaviourSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across behaviour name + body",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntityBehaviour.FTSEntity(), constants.EntityBehaviour.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.Behaviour.Query().Where(entBehaviour.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch behaviour").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"name": r.Name, "body": r.Body},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s %s", h.ID, row["name"])
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

// ── cookbookrecipe ───────────────────────────────────────────────────────

func newCookbookRecipeSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across cookbookrecipe title + body + language",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntityCookbookRecipe.FTSEntity(), constants.EntityCookbookRecipe.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.CookbookRecipe.Query().Where(entCookbookRecipe.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch cookbookrecipe").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"title": r.Title, "body": r.Body, "language": derefStr(r.Language)},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s %s", h.ID, row["title"])
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

// ── incident ─────────────────────────────────────────────────────────────

func newIncidentSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across incident title + body",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntityIncident.FTSEntity(), constants.EntityIncident.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.Incident.Query().Where(entIncident.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch incident").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"title": r.Title, "body": r.Body},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s %s", h.ID, row["title"])
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

// ── suggestion ───────────────────────────────────────────────────────────

func newSuggestionSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across suggestion title + body + status",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntitySuggestion.FTSEntity(), constants.EntitySuggestion.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.Suggestion.Query().Where(entSuggestion.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch suggestion").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"title": r.Title, "body": r.Body, "status_str": r.StatusStr},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s %s", h.ID, row["title"])
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

// ── tastepref ────────────────────────────────────────────────────────────

func newTastePrefSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across tastepref body + scope",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntityTastePref.FTSEntity(), constants.EntityTastePref.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.TastePref.Query().Where(entTastePref.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch tastepref").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"body": r.Body, "scope": derefStr(r.Scope)},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s %s", h.ID, trunc(row["body"].(string), 80))
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

// ── snapshot ─────────────────────────────────────────────────────────────

func newSnapshotSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across snapshot title + body",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntitySnapshot.FTSEntity(), constants.EntitySnapshot.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.Snapshot.Query().Where(entSnapshot.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch snapshot").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"title": r.Title, "body": r.Body},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s %s", h.ID, row["title"])
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

// ── comment ──────────────────────────────────────────────────────────────

// ── handoff ──────────────────────────────────────────────────────────────

func newHandoffSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across handoff body + status",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntityHandoff.FTSEntity(), constants.EntityHandoff.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.Handoff.Query().Where(entHandoff.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch handoff").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"body": r.Body, "status_str": r.StatusStr},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s [%s] %s", h.ID, row["status_str"], trunc(row["body"].(string), 80))
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

// ── mission ──────────────────────────────────────────────────────────────

// ── tasklist ─────────────────────────────────────────────────────────────

// ── plan ─────────────────────────────────────────────────────────────────

// ── workflow ─────────────────────────────────────────────────────────────

func newWorkflowSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across workflow name + body",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntityWorkflow.FTSEntity(), constants.EntityWorkflow.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.Workflow.Query().Where(entWorkflow.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch workflow").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"name": r.Name, "body": r.Body},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s %s", h.ID, row["name"])
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

// ── workspace ────────────────────────────────────────────────────────────

func newWorkspaceSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across workspace name + body",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntityWorkspace.FTSEntity(), constants.EntityWorkspace.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.Workspace.Query().Where(entWorkspace.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch workspace").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"name": r.Name, "body": r.Body},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s %s", h.ID, row["name"])
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

// ── techdoc ──────────────────────────────────────────────────────────────

func newTechDocSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use: "search <query>", Short: "FTS5 search across techdoc name + description + base_url",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return genericSearchRunE(cmd, args, &f, constants.EntityTechDoc.FTSEntity(), constants.EntityTechDoc.SearchKind(),
				func(c *ent.Client, ids []string) ([]entitySearchHit, error) {
					rows, err := c.TechDoc.Query().Where(entTechDoc.IDIn(ids...)).All(cmd.Context())
					if err != nil {
						return nil, errcodes.New(errcodes.Internal, "fetch techdoc").WithCause(err)
					}
					out := make([]entitySearchHit, 0, len(rows))
					for _, r := range rows {
						out = append(out, entitySearchHit{
							ID: r.ID, Row: map[string]any{"name": r.Name, "description": derefStr(r.Description), "base_url": derefStr(r.BaseURL)},
						})
					}
					return out, nil
				},
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s %s", h.ID, row["name"])
				})
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}
