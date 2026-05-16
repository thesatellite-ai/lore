// search.go — `lore memory|rule|hotfix|decision search` (S2.3)
//
// v0.1: simple LIKE-based substring matching via ent predicates
// v0.2 will swap to FTS5 BM25 ranking; the API stays stable
//
// Scope flags (R17 + R35) compose:
//
//	--repo=<name>        scope to specific repo (mount_name or rep_id)
//	--all-repos          all repos in this project
//	--master-only        project-master rows only (repo_id IS NULL)
//	--no-inherit         strict scope; no inheritance from broader scopes
//
// JSON output via --json (schema_version: 1, R25 #3)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"saas/pkg/constants"

	"dbent"
	"dbent/gen/ent"
	entMemory "dbent/gen/ent/memory"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/fts5"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// _ unused import guard for dbent in case all references go away
var _ = dbent.InitDB

type searchFlags struct {
	commonFlags
	allRepos        bool
	masterOnly      bool
	noInherit       bool
	limit           int
	jsonOutput      bool
	includeArchived bool
}

func bindSearchFlags(cmd *cobra.Command, f *searchFlags) {
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().BoolVar(&f.allRepos, constants.FlagAllRepos, false, "search across all repos in this project")
	cmd.Flags().BoolVar(&f.masterOnly, constants.FlagMasterOnly, false, "search only project-master rows")
	cmd.Flags().BoolVar(&f.noInherit, constants.FlagNoInherit, false, "strict scope; no broader-scope inheritance")
	cmd.Flags().IntVar(&f.limit, constants.FlagLimit, 10, "max results (0 = unlimited)")
	cmd.Flags().BoolVar(&f.jsonOutput, constants.FlagJSON, false, "JSON output with stable schema")
	cmd.Flags().BoolVar(&f.includeArchived, constants.FlagIncludeArchived, false, "include soft-archived rows")
}

// memorySearchResult is the JSON wire format for a memory search hit
type memorySearchResult struct {
	ID         string  `json:"id"`
	Body       string  `json:"body"`
	Kind       string  `json:"kind"`
	TrustScore float64 `json:"trust_score"`
	RepoID     string  `json:"repo_id,omitempty"`
	Scope      string  `json:"scope"`
}

// memorySearchEnvelope is the top-level JSON shape (R25 #3 stable schema)
type memorySearchEnvelope struct {
	SchemaVersion int                  `json:"schema_version"`
	Query         string               `json:"query"`
	Count         int                  `json:"count"`
	Results       []memorySearchResult `json:"results"`
}

func newMemorySearchCommand() *cobra.Command {
	f := &searchFlags{}
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search memories by substring",
		Long: `Search memories by case-insensitive substring match against body

Empty query returns the most recent N (default 10)

Scope defaults to current repo (if --repo set) or project-master
See --all-repos, --master-only, --no-inherit for scope variations.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			query := ""
			if len(args) > 0 {
				query = args[0]
			}
			return runMemorySearch(cmd.Context(), f, query)
		},
	}
	bindSearchFlags(cmd, f)
	return cmd
}

func runMemorySearch(ctx context.Context, f *searchFlags, query string) error {
	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer client.Close()

	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}
	repoID, err := resolveRepoID(ctx, client, projectID, rctx.RepoMount)
	if err != nil {
		return err
	}

	q := client.Memory.Query().Where(entMemory.ProjectID(projectID))

	// Scope filter
	switch {
	case f.allRepos:
		// no repo filter
	case f.masterOnly:
		q = q.Where(entMemory.RepoIDIsNil())
	case f.noInherit && repoID != "":
		q = q.Where(entMemory.RepoID(repoID))
	case f.noInherit:
		q = q.Where(entMemory.RepoIDIsNil())
	case repoID != "":
		// Default with --repo: include both repo + master
		q = q.Where(entMemory.Or(
			entMemory.RepoID(repoID),
			entMemory.RepoIDIsNil(),
		))
	default:
		// Default no --repo: master only
		q = q.Where(entMemory.RepoIDIsNil())
	}

	if !f.includeArchived {
		q = q.Where(entMemory.ArchivedAtIsNil())
	}

	// FTS5 path: only when we have a non-empty query, FTS5 schema is healthy,
	// and the raw *sql.DB is recoverable from the ent client. Otherwise fall
	// back to LIKE-substring matching (entMemory.BodyContainsFold)
	var fts5IDs []string
	usedFTS := false
	if query != "" {
		if rawDB := rawDBFromClient(client); rawDB != nil && fts5.Available(ctx, rawDB) {
			hits, err := fts5.Search(ctx, rawDB, projectID, query, f.limit)
			if err == nil {
				usedFTS = true
				fts5IDs = make([]string, len(hits))
				for i, h := range hits {
					fts5IDs[i] = h.MemoryID
				}
			}
			// On FTS5 error (malformed query etc.) we silently degrade to LIKE
			// so the user still gets a result instead of a syntax stub
		}
	}

	if usedFTS {
		if len(fts5IDs) == 0 {
			// FTS5 matched nothing — short-circuit. Print plain "no matches"
			if f.jsonOutput {
				env := memorySearchEnvelope{SchemaVersion: 1, Query: query, Count: 0, Results: []memorySearchResult{}}
				out, _ := json.MarshalIndent(env, "", "  ")
				fmt.Println(string(out))
				return nil
			}
			fmt.Println(style.Muted("(no matches)"))
			return nil
		}
		q = q.Where(entMemory.IDIn(fts5IDs...))
		// Preserve FTS5's BM25 order client-side after fetching
	} else if query != "" {
		q = q.Where(entMemory.BodyContainsFold(query))
	}

	if !usedFTS {
		q = q.Order(ent.Desc(entMemory.FieldID))
	}
	if f.limit > 0 {
		q = q.Limit(f.limit)
	}

	rows, err := q.All(ctx)
	if err == nil && usedFTS && len(fts5IDs) > 1 {
		// Reorder rows to match FTS5 BM25 order
		ordered := make([]*ent.Memory, 0, len(rows))
		byID := make(map[string]*ent.Memory, len(rows))
		for _, r := range rows {
			byID[r.ID] = r
		}
		for _, id := range fts5IDs {
			if r, ok := byID[id]; ok {
				ordered = append(ordered, r)
			}
		}
		rows = ordered
	}
	if err != nil {
		return errcodes.New(errcodes.Internal, "search query").WithCause(err)
	}

	if f.jsonOutput {
		env := memorySearchEnvelope{
			SchemaVersion: 1,
			Query:         query,
			Count:         len(rows),
			Results:       make([]memorySearchResult, 0, len(rows)),
		}
		for _, r := range rows {
			res := memorySearchResult{
				ID:         r.ID,
				Body:       r.Body,
				Kind:       string(r.Kind),
				TrustScore: r.TrustScore,
			}
			if r.RepoID != nil {
				res.RepoID = *r.RepoID
				res.Scope = "repo"
			} else {
				res.Scope = "master"
			}
			env.Results = append(env.Results, res)
		}
		out, _ := json.MarshalIndent(env, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	if len(rows) == 0 {
		fmt.Println(style.Muted("(no matches)"))
		fmt.Println(style.Hint("  hint: try --all-repos, --master-only, or remove --include-archived"))
		return nil
	}
	for _, r := range rows {
		scope := "master"
		if r.RepoID != nil {
			scope = "repo:" + *r.RepoID
			// Surface mount_name when easy; if not in cache, just show ID
		}
		excerpt := r.Body
		if len(excerpt) > 80 {
			excerpt = excerpt[:77] + "..."
		}
		fmt.Printf("%s M-%-3s %s\n", style.ScopeBadge(scope), r.ID, excerpt)
	}
	return nil
}

// Wire memory search subcommand into the memory command
func attachMemorySearch(parent *cobra.Command) {
	parent.AddCommand(newMemorySearchCommand())
}
