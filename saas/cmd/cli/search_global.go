// search_global.go — top-level `lore search "<query>"`
//
// Fans out a full-text MATCH across every text-bearing core table, merges
// the per-table rankings via Reciprocal Rank Fusion (RRF), returns a single
// ranked hit list with entity_table + id + score + snippet.
//
// Reads go through the 0.1.18 core: the repo store's sqlite cache
// (wire.App.Search, FTS5) for the ranking, the engine's rows for the scope
// filters and the hit labels. Rows outside the checkout (store projects,
// global, personal) are listed after the repo's hits, as in v0.1.17.
package main

import (
	"context"
	"fmt"
	"os"
	"saas/pkg/constants"
	"sort"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
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
	// fallback is the terminal text when neither Snippet nor Body is set
	// (a cache that gave no snippet); never in --json, so the v0.1.17
	// envelope is unchanged.
	fallback string
}

// searchLabel says which row field labels a hit and whether it is cut.
type searchLabel struct {
	field string // "" = no Body (memory: v0.1.17 printed only the id)
	trunc bool   // cut to searchBodyMax like v0.1.17's truncBody
}

// globalSearchTarget is one table `lore search` fans out over. The scope
// flags mirror v0.1.17's fts5 helpers (entityHasProjectID / RepoID /
// ArchivedAt): a filter applies only where v0.1.17 applied it, so the same
// rows match.
type globalSearchTarget struct {
	entity      string // the --tables name and the "entity" json key
	table       row.Table
	label       searchLabel
	hasProject  bool
	hasRepo     bool
	hasArchived bool
}

// Search tuning: v0.1.17 values.
const (
	// searchRRFK is the standard Reciprocal Rank Fusion constant.
	searchRRFK = 60.0
	// searchBodyMax is the label length (truncBody) of a body-labelled hit.
	searchBodyMax = 80
	// searchFetch is how many cache hits one table may return before the
	// scope filters (v0.1.17 filtered in SQL before its LIMIT; the cache
	// cannot, so it over-fetches and cuts after filtering).
	searchFetch = 1000
	// Defaults of --limit and --per-table.
	searchDefaultLimit    = 20
	searchDefaultPerTable = 10
)

// Row fields the scope filters read.
const (
	searchFieldProject  = "project_id"
	searchFieldRepo     = "repo_id"
	searchFieldArchived = "archived_at"
)

// globalSearchTargets is every searched table, sorted by entity (the
// v0.1.17 registry order, which also orders ties).
var globalSearchTargets = []globalSearchTarget{
	{entity: "architecturenote", table: schema.TableArchitectureNotes, label: searchLabel{field: "title"}, hasProject: true, hasRepo: true},
	{entity: "behaviour", table: schema.TableBehaviours, label: searchLabel{field: "name"}, hasProject: true},
	{entity: "comment", table: schema.TableComments, label: searchLabel{field: "body", trunc: true}},
	{entity: "cookbookrecipe", table: schema.TableCookbookRecipes, label: searchLabel{field: "title"}, hasProject: true},
	{entity: "decision", table: schema.TableDecisions, label: searchLabel{field: "title"}, hasProject: true, hasRepo: true, hasArchived: true},
	{entity: "handoff", table: schema.TableHandoffs, label: searchLabel{field: "body", trunc: true}, hasProject: true},
	{entity: "hotfix", table: schema.TableHotfixes, label: searchLabel{field: "title"}, hasProject: true, hasRepo: true, hasArchived: true},
	{entity: "incident", table: schema.TableIncidents, label: searchLabel{field: "title"}, hasProject: true},
	{entity: "memory", table: schema.TableMemories, hasProject: true, hasRepo: true, hasArchived: true},
	{entity: "mission", table: schema.TableMissions, label: searchLabel{field: "title"}, hasProject: true},
	{entity: "pattern", table: schema.TablePatterns, label: searchLabel{field: "title"}, hasProject: true, hasRepo: true, hasArchived: true},
	{entity: "plan", table: schema.TablePlans, label: searchLabel{field: "title"}, hasProject: true},
	{entity: "playbook", table: schema.TablePlaybooks, label: searchLabel{field: "name"}, hasProject: true, hasArchived: true},
	{entity: "prompt", table: schema.TablePrompts, label: searchLabel{field: "name"}, hasProject: true, hasArchived: true},
	{entity: "rule", table: schema.TableRules, label: searchLabel{field: "body", trunc: true}, hasProject: true, hasRepo: true, hasArchived: true},
	{entity: "snapshot", table: schema.TableSnapshots, label: searchLabel{field: "title"}, hasProject: true, hasArchived: true},
	{entity: "suggestion", table: schema.TableSuggestions, label: searchLabel{field: "title"}, hasProject: true},
	{entity: "task", table: schema.TableTasks, label: searchLabel{field: "title"}, hasProject: true, hasRepo: true},
	{entity: "tasklist", table: schema.TableTasklists, label: searchLabel{field: "title"}, hasProject: true},
	{entity: "tastepref", table: schema.TableTastePrefs, label: searchLabel{field: "body", trunc: true}, hasProject: true},
	{entity: "techdoc", table: schema.TableTechDocs, label: searchLabel{field: "name"}, hasProject: true},
	{entity: "workflow", table: schema.TableWorkflows, label: searchLabel{field: "name"}, hasProject: true},
	{entity: "workspace", table: schema.TableWorkspaces, label: searchLabel{field: "name"}, hasProject: true},
}

// globalSearchScope is the resolved --project / --repo / scope flags.
type globalSearchScope struct {
	projectID       string
	repoID          string
	allRepos        bool
	masterOnly      bool
	includeArchived bool
}

// keeps reports whether r passes t's v0.1.17 filters under sc.
func (sc globalSearchScope) keeps(t globalSearchTarget, r row.Row) bool {
	if r.Deleted {
		return false
	}
	if t.hasProject && sc.projectID != "" {
		if p := searchField(r, searchFieldProject); p != "" && p != sc.projectID {
			return false
		}
	}
	if t.hasRepo && !sc.allRepos {
		repo := searchField(r, searchFieldRepo)
		switch {
		case sc.masterOnly:
			if repo != "" {
				return false
			}
		case sc.repoID != "":
			if repo != "" && repo != sc.repoID {
				return false
			}
		}
	}
	if t.hasArchived && !sc.includeArchived && searchField(r, searchFieldArchived) != "" {
		return false
	}
	return true
}

// searchField is the text form of a row field ("" when absent or null).
func searchField(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
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
			if err := requireSearchQuery(query); err != nil {
				return err
			}
			ctx := cmd.Context()
			targets, err := globalSearchTargetsFor(tables)
			if err != nil {
				return err
			}
			if perTableLimit <= 0 {
				perTableLimit = searchDefaultPerTable
			}
			if limit <= 0 {
				limit = searchDefaultLimit
			}
			_ = noInherit // currently a no-op shape; reserved for strict-scope flag
			sc, err := resolveGlobalSearchScope(ctx, &f)
			if err != nil {
				return err
			}
			sc.allRepos, sc.masterOnly, sc.includeArchived = allRepos, masterOnly, includeArchived
			eng, err := coreApp.Engine(ctx)
			if err != nil {
				return err
			}

			// RRF merge. Per-table rank 0 is best; score = sum(1 / (k + rank)).
			merged := make([]globalSearchHit, 0, limit)
			matchedTables := 0
			for _, t := range targets {
				hits, err := searchCoreTable(ctx, eng, t, query, sc, perTableLimit)
				if err != nil {
					return err
				}
				if len(hits) == 0 {
					continue
				}
				matchedTables++
				for rank, h := range hits {
					h.Score = 1.0 / (searchRRFK + float64(rank))
					merged = append(merged, h)
				}
			}
			// Stable: equal scores keep table order, as close to v0.1.17's
			// map-order tie as a deterministic output can be.
			sort.SliceStable(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
			if len(merged) > limit {
				merged = merged[:limit]
			}

			// Rows outside this checkout (linked store projects, global,
			// personal; F20) are matched as plain text and listed after the
			// repo's hits, each with its source label.
			extText := plainSearchText(query)
			if jsonOut {
				printJSON(constants.KindSearchGlobal, map[string]any{
					"query":    query,
					"count":    len(merged),
					"hits":     merged,
					"external": externalJSONRows(ctx, "", extText),
				}, len(merged))
				return nil
			}
			if len(merged) == 0 {
				var ext strings.Builder
				if printExternalRows(ctx, &ext, "", extText) > 0 {
					fmt.Printf("No hits in this repo; outside this checkout:\n\n%s", ext.String())
					return nil
				}
				fmt.Println(style.Muted("(no matches)"))
				return nil
			}
			defer func() {
				var ext strings.Builder
				if printExternalRows(ctx, &ext, "", extText) > 0 {
					fmt.Printf("\nOutside this checkout:\n\n%s", ext.String())
				}
			}()
			fmt.Printf("Found %d %s for %q across %d %s:\n\n", len(merged), plural(len(merged), "hit", "hits"), query, matchedTables, plural(matchedTables, "entity type", "entity types"))
			for _, h := range merged {
				kindTag := fmt.Sprintf("[%-10s]", h.Entity)
				pretty := h.Pretty
				if pretty == "" {
					pretty = h.ID
				}
				body := plainSnippet(h.Snippet)
				if body == "" {
					body = h.Body
				}
				if body == "" {
					body = h.fallback
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
	// ds:def id=search-defaults-87uy20io owner=@khanakia stability=stable desc="lore search defaults (--limit 20, --per-table 10)"
	cmd.Flags().IntVar(&limit, constants.FlagLimit, searchDefaultLimit, "max total hits after merge (default 20)")
	cmd.Flags().IntVar(&perTableLimit, "per-table", searchDefaultPerTable, "per-entity top-K before merge (default 10)")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON envelope output")
	cmd.Flags().BoolVar(&includeArchived, constants.FlagIncludeArchived, false, "include archived rows")
	cmd.Flags().BoolVar(&allRepos, constants.FlagAllRepos, false, "search every repo (ignore --repo)")
	cmd.Flags().BoolVar(&masterOnly, constants.FlagMasterOnly, false, "only master-scoped rows")
	cmd.Flags().BoolVar(&noInherit, constants.FlagNoInherit, false, "strict scope (reserved)")
	return cmd
}

// globalSearchTargetsFor narrows the searched tables to --tables (entity
// names); none matching is E_INVALID_INPUT, as v0.1.17 refused it.
func globalSearchTargetsFor(tables []string) ([]globalSearchTarget, error) {
	if len(tables) == 0 {
		return globalSearchTargets, nil
	}
	wanted := make(map[string]bool, len(tables))
	for _, t := range tables {
		wanted[strings.TrimSpace(t)] = true
	}
	out := make([]globalSearchTarget, 0, len(tables))
	for _, t := range globalSearchTargets {
		if wanted[t.entity] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, errcodes.New(errcodes.InvalidInput, "none of --tables matched a known entity")
	}
	return out, nil
}

// resolveGlobalSearchScope resolves --project and --repo against the repo
// store: the project is the repo's one live project (or the --project id /
// name, which must be it), the repo a rep_ id or mount name.
func resolveGlobalSearchScope(ctx context.Context, f *commonFlags) (globalSearchScope, error) {
	if f.flagReadOnly {
		_ = os.Setenv("LORE_READ_ONLY", "1")
	}
	if coreApp == nil {
		return globalSearchScope{}, errcodes.New(errcodes.Internal, "the core is not available in this process")
	}
	rctx, err := projresolve.Resolve(projresolve.Inputs{FlagDB: f.flagDB, FlagProject: f.flagProject, FlagRepo: f.flagRepo})
	if err != nil {
		return globalSearchScope{}, mapResolveError(err)
	}
	pid, err := coreApp.Project(ctx)
	if err != nil {
		return globalSearchScope{}, err
	}
	sc := globalSearchScope{projectID: pid.String()}
	if want := f.flagProject; want != "" && want != sc.projectID {
		_, p, err := coreApp.Get(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TableProjects, ID: pid.Any()})
		if err != nil || searchField(p, "name") != want {
			return globalSearchScope{}, errcodes.New(errcodes.ProjectNotFound,
				fmt.Sprintf("no project %q in this repository", want)).
				WithHint("run `lore project list` to see available IDs")
		}
	}
	if rctx.RepoMount != "" {
		rid, err := coreApp.RepoID(ctx, rctx.RepoMount)
		if err != nil {
			return globalSearchScope{}, errcodes.New(errcodes.RepoNotFound,
				fmt.Sprintf("no repo with mount_name %q in current project", rctx.RepoMount))
		}
		sc.repoID = rid.String()
	}
	return sc, nil
}

// searchCoreTable runs query over t in the repo store's cache and returns
// t's in-scope hits, best first, at most perTable, labelled from the rows.
// A query FTS5 rejects as syntax is retried once with every word quoted
// (v0.1.17's fallback); a second refusal is E_INVALID_INPUT.
func searchCoreTable(ctx context.Context, eng *engine.Engine, t globalSearchTarget, query string, sc globalSearchScope, perTable int) ([]globalSearchHit, error) {
	hits, err := coreApp.Search(ctx, t.table, query, searchFetch)
	if errs.Has(err, errs.InvalidInput) {
		if quoted := quoteSearchTerms(query); quoted != "" && quoted != query {
			hits, err = coreApp.Search(ctx, t.table, quoted, searchFetch)
		}
	}
	if err != nil {
		if errs.Has(err, errs.InvalidInput) {
			return nil, errcodes.New(errcodes.InvalidInput,
				fmt.Sprintf("fts5 query against %s: %v", t.entity, err)).
				WithHint("FTS5 syntax: 'foo*' / '\"phrase\"' / 'a OR b' / 'a NOT b'")
		}
		return nil, err
	}
	var rows map[ids.AnyID]row.Row // loaded on the first repo hit
	out := make([]globalSearchHit, 0, perTable)
	for _, h := range hits {
		if len(out) == perTable {
			break
		}
		if h.Source != store.SourceRepo {
			continue // listed under "outside this checkout"
		}
		if rows == nil {
			list, err := eng.List(ctx, store.SourceRepo, t.table)
			if err != nil {
				return nil, err
			}
			rows = make(map[ids.AnyID]row.Row, len(list))
			for _, r := range list {
				rows[r.ID] = r
			}
		}
		r, ok := rows[h.ID]
		if !ok || !sc.keeps(t, r) {
			continue // gone since the cache was refreshed, or out of scope
		}
		out = append(out, globalHitOf(t, h.ID.String(), h.Snippet, r))
	}
	return out, nil
}

// globalHitOf is a hit labelled like v0.1.17's hydrateGlobalHits: pretty =
// the id, Body = the table's label field (body-labelled ones cut).
func globalHitOf(t globalSearchTarget, id, snippet string, r row.Row) globalSearchHit {
	h := globalSearchHit{EntityTable: t.table.String(), Entity: t.entity, ID: id, Pretty: id, Snippet: snippet}
	if t.label.field != "" {
		h.Body = searchField(r, t.label.field)
		if t.label.trunc {
			h.Body = truncBody(h.Body, searchBodyMax)
		}
	}
	h.fallback = truncBody(strings.Join(strings.Fields(searchField(r, "body")), " "), searchBodyMax)
	return h
}

// quoteSearchTerms rewrites free text as an FTS5 query matching every word
// literally (each word a quoted phrase, embedded quotes doubled), the
// retry for text such as "round-trip" or "a:b" that is not FTS5 syntax.
func quoteSearchTerms(q string) string {
	fields := strings.Fields(q)
	for i, f := range fields {
		fields[i] = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
	}
	return strings.Join(fields, " ")
}

// plainSearchText turns an FTS5 query into the plain text matched against
// rows outside the checkout: operators, quotes and wildcards dropped.
func plainSearchText(q string) string {
	r := strings.NewReplacer(`"`, " ", "*", " ", "(", " ", ")", " ")
	var words []string
	for _, w := range strings.Fields(r.Replace(q)) {
		switch w {
		case "AND", "OR", "NOT", "NEAR":
			continue
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

func truncBody(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// snippetMarks are the match markers FTS5 snippet() wraps hits in
// (the cache's FTS5 snippet). They stay in --json output, where a caller may render
// them, and are removed for the terminal, where they would print raw.
var snippetMarks = strings.NewReplacer("<b>", "", "</b>", "")

// plainSnippet is a snippet as the terminal shows it.
func plainSnippet(s string) string { return snippetMarks.Replace(s) }
