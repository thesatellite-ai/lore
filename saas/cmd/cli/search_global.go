// search_global.go — top-level `lore search "<query>"`
//
// Fans out an FTS5 MATCH across every entity in the Registry, merges per-
// table rankings via Reciprocal Rank Fusion (RRF), returns a single ranked
// hit list with entity_table + id + id + score + snippet
//
// The bottleneck that motivated this: agents would otherwise need to run
// `<kind> search "X"` once per entity type to find captured knowledge
// One call here covers all 23 text-bearing entities
package main

import (
	"context"
	"fmt"
	"saas/pkg/constants"
	"sort"
	"strings"

	"dbent/gen/ent"
	entArchitectureNote "dbent/gen/ent/architecturenote"
	entBehaviour "dbent/gen/ent/behaviour"
	entComment "dbent/gen/ent/comment"
	entCookbookRecipe "dbent/gen/ent/cookbookrecipe"
	entDecision "dbent/gen/ent/decision"
	entHandoff "dbent/gen/ent/handoff"
	entHotfix "dbent/gen/ent/hotfix"
	entIncident "dbent/gen/ent/incident"
	entMemory "dbent/gen/ent/memory"
	entMission "dbent/gen/ent/mission"
	entPattern "dbent/gen/ent/pattern"
	entPlan "dbent/gen/ent/plan"
	entPlaybook "dbent/gen/ent/playbook"
	entPrompt "dbent/gen/ent/prompt"
	entRule "dbent/gen/ent/rule"
	entSnapshot "dbent/gen/ent/snapshot"
	entSuggestion "dbent/gen/ent/suggestion"
	entTask "dbent/gen/ent/task"
	entTaskList "dbent/gen/ent/tasklist"
	entTastePref "dbent/gen/ent/tastepref"
	entTechDoc "dbent/gen/ent/techdoc"
	entWorkflow "dbent/gen/ent/workflow"
	entWorkspace "dbent/gen/ent/workspace"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/fts5"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// globalSearchHit is one cross-entity match
type globalSearchHit struct {
	EntityTable string  `json:"entity_table"`
	Entity      string  `json:"entity"` // singular noun ("memory", "rule", …)
	ID          string  `json:"id"`
	Pretty      string  `json:"pretty,omitempty"` // "R-15", "M-3" — for display
	Score       float64 `json:"score"`
	Snippet     string  `json:"snippet,omitempty"`
	Body        string  `json:"body,omitempty"` // short body excerpt for plain-text rendering
}

// newGlobalSearchCommand wires `lore search "<query>"` (the top-level
// fan-out search). When invoked with no positional arg, falls through to
// the search-admin subcommand surface (status, rebuild) registered alongside
func newGlobalSearchCommand() *cobra.Command {
	var f commonFlags
	var tables []string
	var limit int
	var perTableLimit int
	var jsonOut bool
	var includeArchived bool
	var allRepos, masterOnly, noInherit bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Unified FTS5 search across all entities (fans out per-entity + RRF-merges)",
		Long: `One-call FTS5 lookup across every text-bearing entity. Returns ranked
hits with entity_table + id + score + snippet — the canonical retrieval
verb for agents before responding

Per-entity search still works (` + "`lore task search \"X\"`" + ` etc.) when
you want to scope to one type. This is the cross-entity hammer.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				// No query → show help (admin subcommands are still available)
				return cmd.Help()
			}
			query := args[0]
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			repoID, err := resolveRepoID(cmd.Context(), client, projectID, rctx.RepoMount)
			if err != nil {
				return err
			}
			rawDB := rawDBFromClient(client)
			if rawDB == nil {
				return errcodes.New(errcodes.Internal, "raw DB unavailable")
			}
			if !fts5.Available(cmd.Context(), rawDB) {
				return errcodes.New(errcodes.Internal,
					"FTS5 not compiled into this binary").
					WithHint("rebuild with `task aicoder:build` (sqlite_fts5 tag)")
			}

			// Resolve target entity list
			targets := fts5.Registry
			if len(tables) > 0 {
				wanted := make(map[string]bool, len(tables))
				for _, t := range tables {
					wanted[strings.TrimSpace(t)] = true
				}
				filtered := make([]fts5.Config, 0, len(tables))
				for _, c := range fts5.Registry {
					if wanted[c.Entity] {
						filtered = append(filtered, c)
					}
				}
				if len(filtered) == 0 {
					return errcodes.New(errcodes.InvalidInput,
						"none of --tables matched a known entity")
				}
				targets = filtered
			}

			if perTableLimit <= 0 {
				perTableLimit = 10
			}
			if limit <= 0 {
				limit = 20
			}

			opts := fts5.SearchOptions{
				ProjectID:       projectID,
				RepoID:          repoID,
				AllRepos:        allRepos,
				MasterOnly:      masterOnly,
				IncludeArchived: includeArchived,
				Limit:           perTableLimit,
			}
			_ = noInherit // currently a no-op shape; reserved for strict-scope flag

			// Fan out per-entity FTS5 query
			type tableHits struct {
				cfg  fts5.Config
				hits []fts5.EntityHit
			}
			perTable := make([]tableHits, 0, len(targets))
			for _, cfg := range targets {
				hits, err := fts5.SearchEntity(cmd.Context(), rawDB, cfg, query, opts)
				if err != nil {
					// Bad FTS5 syntax errors only on the first call; subsequent
					// calls would all repeat the error. Surface the first one
					// and bail
					return errcodes.New(errcodes.InvalidInput,
						fmt.Sprintf("fts5 query against %s: %v", cfg.Entity, err)).
						WithHint("FTS5 syntax: 'foo*' / '\"phrase\"' / 'a OR b' / 'a NOT b'")
				}
				if len(hits) > 0 {
					perTable = append(perTable, tableHits{cfg: cfg, hits: hits})
				}
			}

			// RRF merge. Per-table rank 0 is best; score = sum(1 / (k + rank))
			// where k=60 (standard)
			const rrfK = 60.0
			rrfScores := make(map[string]float64)        // key = entity_table + "|" + id
			meta := make(map[string]globalSearchHit, 64) // key → first-seen metadata
			for _, t := range perTable {
				for rank, h := range t.hits {
					key := t.cfg.Table + "|" + h.ID
					rrfScores[key] += 1.0 / (rrfK + float64(rank))
					if _, seen := meta[key]; !seen {
						meta[key] = globalSearchHit{
							EntityTable: t.cfg.Table,
							Entity:      t.cfg.Entity,
							ID:          h.ID,
							Snippet:     h.Snippet,
						}
					}
				}
			}

			// Materialize + sort by RRF score desc
			merged := make([]globalSearchHit, 0, len(rrfScores))
			for key, score := range rrfScores {
				m := meta[key]
				m.Score = score
				merged = append(merged, m)
			}
			sort.Slice(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
			if len(merged) > limit {
				merged = merged[:limit]
			}

			// Hydrate id + pretty form + body excerpt by ID lookup
			// per entity (single batch per entity)
			hydrateGlobalHits(cmd.Context(), client, merged)

			if jsonOut {
				printJSON(constants.KindSearchGlobal, map[string]any{
					"query": query,
					"count": len(merged),
					"hits":  merged,
				}, len(merged))
				return nil
			}
			if len(merged) == 0 {
				fmt.Println(style.Muted("(no matches)"))
				return nil
			}
			fmt.Printf("Found %d hits for %q across %d entity types:\n\n", len(merged), query, len(perTable))
			for _, h := range merged {
				kindTag := fmt.Sprintf("[%-10s]", h.Entity)
				pretty := h.Pretty
				if pretty == "" {
					pretty = h.ID
				}
				body := h.Snippet
				if body == "" {
					body = h.Body
				}
				if len(body) > 100 {
					body = body[:97] + "..."
				}
				fmt.Printf("%s %-7s %s  (score=%.3f)\n", style.Code(kindTag), pretty, body, h.Score)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringSliceVar(&tables, "tables", nil, "restrict to these entity types (e.g. memory,rule,decision)")
	cmd.Flags().IntVar(&limit, constants.FlagLimit, 20, "max total hits after merge (default 20)")
	cmd.Flags().IntVar(&perTableLimit, "per-table", 10, "per-entity top-K before merge (default 10)")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON envelope output")
	cmd.Flags().BoolVar(&includeArchived, constants.FlagIncludeArchived, false, "include archived rows")
	cmd.Flags().BoolVar(&allRepos, constants.FlagAllRepos, false, "search every repo (ignore --repo)")
	cmd.Flags().BoolVar(&masterOnly, constants.FlagMasterOnly, false, "only master-scoped rows")
	cmd.Flags().BoolVar(&noInherit, constants.FlagNoInherit, false, "strict scope (reserved)")
	return cmd
}

// hydrateGlobalHits fetches per-entity rows by ID to fill id,
// pretty-form label, and a short body excerpt. Avoids per-hit DB calls
// by grouping IDs per entity table
func hydrateGlobalHits(ctx context.Context, client *ent.Client, hits []globalSearchHit) {
	// Group hit indices by entity
	byEntity := make(map[string][]int)
	for i, h := range hits {
		byEntity[h.Entity] = append(byEntity[h.Entity], i)
	}

	// Per-entity batch fetch
	for entity, indices := range byEntity {
		ids := make([]string, 0, len(indices))
		for _, i := range indices {
			ids = append(ids, hits[i].ID)
		}
		switch entity {
		case "memory":
			rows, _ := client.Memory.Query().Where(entMemory.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].Pretty = r.ID
					}
				}
			}
		case "rule":
			rows, _ := client.Rule.Query().Where(entRule.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = truncBody(r.Body, 80)
					}
				}
			}
		case "decision":
			rows, _ := client.Decision.Query().Where(entDecision.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "hotfix":
			rows, _ := client.Hotfix.Query().Where(entHotfix.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "pattern":
			rows, _ := client.Pattern.Query().Where(entPattern.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "task":
			rows, _ := client.Task.Query().Where(entTask.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "mission":
			rows, _ := client.Mission.Query().Where(entMission.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "tasklist":
			rows, _ := client.TaskList.Query().Where(entTaskList.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "plan":
			rows, _ := client.Plan.Query().Where(entPlan.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "playbook":
			rows, _ := client.Playbook.Query().Where(entPlaybook.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Name
					}
				}
			}
		case "prompt":
			rows, _ := client.Prompt.Query().Where(entPrompt.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Name
					}
				}
			}
		case "architecturenote":
			rows, _ := client.ArchitectureNote.Query().Where(entArchitectureNote.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "behaviour":
			rows, _ := client.Behaviour.Query().Where(entBehaviour.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Name
					}
				}
			}
		case "cookbookrecipe":
			rows, _ := client.CookbookRecipe.Query().Where(entCookbookRecipe.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "incident":
			rows, _ := client.Incident.Query().Where(entIncident.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "suggestion":
			rows, _ := client.Suggestion.Query().Where(entSuggestion.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "tastepref":
			rows, _ := client.TastePref.Query().Where(entTastePref.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = truncBody(r.Body, 80)
					}
				}
			}
		case "snapshot":
			rows, _ := client.Snapshot.Query().Where(entSnapshot.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Title
					}
				}
			}
		case "handoff":
			rows, _ := client.Handoff.Query().Where(entHandoff.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = truncBody(r.Body, 80)
					}
				}
			}
		case "comment":
			rows, _ := client.Comment.Query().Where(entComment.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].Pretty = r.ID
						hits[i].Body = truncBody(r.Body, 80)
					}
				}
			}
		case "workflow":
			rows, _ := client.Workflow.Query().Where(entWorkflow.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Name
					}
				}
			}
		case "workspace":
			rows, _ := client.Workspace.Query().Where(entWorkspace.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Name
					}
				}
			}
		case "techdoc":
			rows, _ := client.TechDoc.Query().Where(entTechDoc.IDIn(ids...)).All(ctx)
			for _, r := range rows {
				for _, i := range indices {
					if hits[i].ID == r.ID {
						hits[i].ID = r.ID
						hits[i].Pretty = fmt.Sprintf("%s", r.ID)
						hits[i].Body = r.Name
					}
				}
			}
		}
	}
}

func truncBody(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
