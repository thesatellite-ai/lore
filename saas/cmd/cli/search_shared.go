// search_shared.go — common scaffolding for per-entity `<kind> search` cmds
//
// Each per-entity search command:
//  1. Binds the standard flag set (entitySearchFlags below)
//  2. Resolves project + repo scope
//  3. Calls fts5.SearchEntity → ranked rowids + snippets
//  4. Fetches rows by ID via ent
//  5. Eager-loads relations (per-entity)
//  6. Emits the canonical envelope (entitySearchEnvelope)
//
// Phase 1c will wire ~20 per-entity wrappers on top of this helper. Each
// wrapper is ~30 LOC of boilerplate plus the per-entity eager-load logic
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"saas/pkg/constants"

	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/fts5"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// entitySearchFlags is the universal flag set bound to every `<kind> search`
type entitySearchFlags struct {
	commonFlags
	columns         []string
	limit           int
	jsonOutput      bool
	includeArchived bool
	allRepos        bool
	masterOnly      bool
	noInherit       bool
}

// bindEntitySearchFlags attaches the standard search-flag set to a cobra cmd
// Each per-entity search file calls this and then optionally adds its own
// status/severity/kind filters on top
func bindEntitySearchFlags(cmd *cobra.Command, f *entitySearchFlags) {
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringSliceVar(&f.columns, constants.FlagColumn, nil, "restrict MATCH to these FTS5 columns (repeatable)")
	cmd.Flags().IntVar(&f.limit, constants.FlagLimit, 20, "max hits to return")
	cmd.Flags().BoolVar(&f.jsonOutput, constants.FlagJSON, false, "JSON envelope (stable schema)")
	cmd.Flags().BoolVar(&f.includeArchived, constants.FlagIncludeArchived, false, "include archived rows")
	cmd.Flags().BoolVar(&f.allRepos, constants.FlagAllRepos, false, "search across every repo (ignore --repo)")
	cmd.Flags().BoolVar(&f.masterOnly, constants.FlagMasterOnly, false, "only master-scoped rows (repo_id IS NULL)")
	cmd.Flags().BoolVar(&f.noInherit, constants.FlagNoInherit, false, "strict scope: --repo only, no master inheritance")
}

// resolveSearchOpts converts the cobra flag struct into fts5.SearchOptions
// after performing the project / repo lookups
func resolveSearchOpts(ctx context.Context, client *ent.Client, rctx *projresolveContext, f *entitySearchFlags) (fts5.SearchOptions, error) {
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return fts5.SearchOptions{}, err
	}
	repoID, err := resolveRepoID(ctx, client, projectID, rctx.RepoMount)
	if err != nil {
		return fts5.SearchOptions{}, err
	}
	opts := fts5.SearchOptions{
		ProjectID:       projectID,
		RepoID:          repoID,
		AllRepos:        f.allRepos,
		MasterOnly:      f.masterOnly,
		IncludeArchived: f.includeArchived,
		Columns:         f.columns,
		Limit:           f.limit,
	}
	return opts, nil
}

// projresolveContext is a thin alias so the helper signature doesn't pull
// the full projresolve package in. The fields needed match the existing
// resolveContext() return
type projresolveContext struct {
	ProjectID string
	RepoMount string
}

// entitySearchEnvelope is the canonical JSON wire format for every
// `<kind> search --json` output. Every hit carries:
//
//   - id           opaque ID
//   - id  pretty-form number (when applicable)
//   - score        flipped BM25 (higher = better)
//   - snippet      FTS5 snippet() output with <b>...</b> markers
//   - row          full entity row (per-entity columns)
//   - relations    eager-loaded references (per-entity structure)
//
// Per-entity wrappers populate row + relations; this helper handles the
// envelope, scoring, and snippet wiring
type entitySearchEnvelope struct {
	SchemaVersion int               `json:"schema_version"`
	Kind          string            `json:"kind"`
	Query         string            `json:"query"`
	Count         int               `json:"count"`
	Data          []entitySearchHit `json:"data"`
}

type entitySearchHit struct {
	ID        string  `json:"id"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet,omitempty"`
	Row       any     `json:"row"`
	Relations any     `json:"relations,omitempty"`
}

// printSearchEnvelope writes the envelope to stdout in JSON or human form
func printSearchEnvelope(jsonOutput bool, kind, query string, hits []entitySearchHit, humanLine func(h entitySearchHit) string) {
	if jsonOutput {
		env := entitySearchEnvelope{
			SchemaVersion: 1,
			Kind:          kind,
			Query:         query,
			Count:         len(hits),
			Data:          hits,
		}
		out, err := json.MarshalIndent(env, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "json marshal:", err)
			return
		}
		fmt.Println(string(out))
		return
	}
	if len(hits) == 0 {
		fmt.Println(style.Muted("(no matches)"))
		return
	}
	for _, h := range hits {
		fmt.Println(humanLine(h))
	}
}

// runFTSAgainst is the inner-loop helper every per-entity search uses:
// resolves scope, runs FTS5 against the entity's Config, and returns the
// ranked hits ready for ent-side fetching
//
// Returns (nil, nil) on FTS5 unavailable — caller can degrade to LIKE
func runFTSAgainst(ctx context.Context, client *ent.Client, rctx *projresolveContext, entity, query string, f *entitySearchFlags) ([]fts5.EntityHit, error) {
	cfg, ok := fts5.FindConfig(entity)
	if !ok {
		return nil, errcodes.New(errcodes.InvalidInput, "unknown entity "+entity)
	}
	opts, err := resolveSearchOpts(ctx, client, rctx, f)
	if err != nil {
		return nil, err
	}
	rawDB := rawDBFromClient(client)
	if rawDB == nil {
		return nil, errcodes.New(errcodes.Internal, "raw DB unavailable")
	}
	hits, err := fts5.SearchEntity(ctx, rawDB, cfg, query, opts)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "fts5 search").WithCause(err)
	}
	return hits, nil
}

// flipSign returns the BM25 score in "higher = better" form for envelopes
// Pure passthrough today (SearchEntity already flips); kept as a named
// helper for callers that consume EntityHit.BM25 directly
func flipSign(score float64) float64 { return score }
