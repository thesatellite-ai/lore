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
	"slices"
	"sort"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/metaquery"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
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
	// Source is the `_source` object of a hit from an attached store
	// (FLAG1: {store, store_id, folder, folder_id}); zero for a repo hit,
	// so repo hits' --json bytes are unchanged (r2-source).
	Source store.SourceRef `json:"_source,omitzero"`
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
	// resources (RES 3.12, R8): name, kind name and non-sensitive field values (the cache's resources FTS)
	{entity: searchEntityResource, table: schema.TableResources, label: searchLabel{field: "name"}, hasArchived: true},
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

			// METADATA 3.17: key:value / key=value / key? on a visible meta
			// key are exact conditions (row_meta); then field:value terms
			// naming a resource field (RES 3.12); the rest is free text,
			// phrase-quoted where FTS5 would read - . : as syntax.
			mconds, rest, msens, err := metaSearchTerms(ctx, query)
			if err != nil {
				return err
			}
			var conds []resourceCond
			text := rest
			if len(mconds) == 0 {
				// OV39-META-FIRST: with meta conditions the resource field
				// path is not taken (a name that is both is a meta key here)
				if conds, text, err = resourceFieldTerms(ctx, rest); err != nil {
					return err
				}
			}
			ftsText := metaquery.FTS(metaquery.Parse(text, noMetaKeys))

			// RRF merge. Per-table rank 0 is best; score = sum(1 / (k + rank)).
			merged := make([]globalSearchHit, 0, limit)
			matchedTables := 0
			for _, t := range targets {
				var hits []globalSearchHit
				var err error
				switch {
				case len(mconds) > 0:
					hits, err = searchMetaTable(ctx, eng, t, mconds, msens, ftsText, sc, perTableLimit)
				case len(conds) > 0 && t.entity == searchEntityResource:
					hits, err = searchResourceFields(ctx, eng, t, conds, text, sc, perTableLimit)
				case len(conds) > 0 && text == "":
					continue // only field conditions: nothing for other tables
				default:
					hits, err = searchCoreTable(ctx, eng, t, ftsText, sc, perTableLimit)
				}
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
			// Store rows the core already listed are not listed twice.
			listed := map[string]bool{}
			for _, h := range merged {
				if h.Source.Store != "" {
					listed[h.ID] = true
				}
			}
			if jsonOut {
				printJSON(constants.KindSearchGlobal, map[string]any{
					"query":    query,
					"count":    len(merged),
					"hits":     merged,
					"external": externalJSONRowsExcept(ctx, "", extText, listed),
				}, len(merged))
				return nil
			}
			if len(merged) == 0 {
				var ext strings.Builder
				if printExternalRowsExcept(ctx, &ext, "", extText, listed) > 0 {
					fmt.Printf("No hits in this repo; outside this checkout:\n\n%s", ext.String())
					return nil
				}
				fmt.Println(style.Muted("(no matches)"))
				return nil
			}
			defer func() {
				var ext strings.Builder
				if printExternalRowsExcept(ctx, &ext, "", extText, listed) > 0 {
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
				if h.Source.Store != "" {
					body += "  " + style.Muted(storeHitLabel(h.Source))
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
	// A fresh clone (.lore/data, no lore.db yet) is a project too: build the
	// read copy first, as resolveContext does for every v0.1.17 command,
	// or a clone whose first command is search is "not a lore project".
	if err := maybeMaterialize(ctx, f); err != nil {
		return globalSearchScope{}, materializeError(err)
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
	rows := map[store.Source]map[ids.AnyID]row.Row{} // per store, loaded on its first hit
	out := make([]globalSearchHit, 0, perTable)
	for _, h := range hits {
		if len(out) == perTable {
			break
		}
		byID, ok := rows[h.Source]
		if !ok {
			list, err := eng.List(ctx, h.Source, t.table)
			if err != nil {
				return nil, err
			}
			byID = make(map[ids.AnyID]row.Row, len(list))
			for _, r := range list {
				byID[r.ID] = r
			}
			rows[h.Source] = byID
		}
		r, ok := byID[h.ID]
		if !ok {
			continue // gone since the cache was refreshed
		}
		if h.Source == store.SourceRepo {
			if !sc.keeps(t, r) {
				continue // out of scope
			}
			out = append(out, globalHitOf(t, h.ID.String(), h.Snippet, r))
			continue
		}
		// r2-source: a row of an attached store (an org store folder, me,
		// team) is a hit too, labelled with its FLAG1 _source; the repo's
		// project/repo scope does not apply to another store's rows, the
		// deleted and archived filters do.
		if !sc.keepsStoreRow(t, r) {
			continue
		}
		hit := globalHitOf(t, h.ID.String(), h.Snippet, r)
		hit.Source = coreApp.DescribeSource(h.Source)
		out = append(out, hit)
	}
	return out, nil
}

// storeHitLabel is the bracketed store label of a store hit ("[agency]",
// "[agency/acme-hub]").
func storeHitLabel(src store.SourceRef) string {
	if src.Folder != "" {
		return "[" + src.Store + "/" + src.Folder + "]"
	}
	return "[" + src.Store + "]"
}

// keepsStoreRow is keeps for a row of an attached store: deleted rows and
// (without --include-archived) archived rows are left out.
func (sc globalSearchScope) keepsStoreRow(t globalSearchTarget, r row.Row) bool {
	if r.Deleted {
		return false
	}
	return !t.hasArchived || sc.includeArchived || searchField(r, searchFieldArchived) == ""
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

// searchEntityResource is the resources entry of the fan-out.
const searchEntityResource = "resource"

// resourceCond is one field:value term of a search: a resource field name
// and the value it must equal (case-insensitive, as text). sensitive is
// set when any kind in the lens marks the field sensitive (fail closed on
// a name shared by kinds): the term still matches the real value, but the
// value is never printed (OV34/OV39-SENSCOND).
type resourceCond struct {
	field, value string
	sensitive    bool
}

// searchMask is what a search prints in place of a sensitive value a term
// matched on (OV34/OV39-SENSCOND; the same mark as logic/meta.Show and the
// masked lens).
const searchMask = "•••"

// saidValue is "name: value" for a hit's snippet, the value masked when
// the term is on a sensitive field or key (OV34/OV39-SENSCOND).
func saidValue(name, v string, sensitive bool) string {
	if sensitive {
		v = searchMask
	}
	return name + ": " + v
}

// resourceFieldTerms splits query (RES 3.12: "field:value in a search
// matches resource fields") into conditions, for every term name:value or
// name=value whose name is a field of some kind in this repo's view, and
// the remaining free text. With no such term it returns no conditions and
// the query unchanged. A condition on a sensitive field matches the real
// value and is printed masked (OV34/OV39-SENSCOND).
func resourceFieldTerms(ctx context.Context, query string) ([]resourceCond, string, error) {
	rest := strings.Fields(query)
	if !slices.ContainsFunc(rest, func(w string) bool { i := strings.IndexAny(w, ":="); return i > 0 && i < len(w)-1 }) {
		return nil, query, nil
	}
	known, err := resourceFieldNames(ctx)
	if err != nil {
		return nil, "", err
	}
	var conds []resourceCond
	text := rest[:0:0]
	for _, w := range rest {
		i := strings.IndexAny(w, ":=")
		if sens, ok := known[w[:i]]; i > 0 && i < len(w)-1 && ok {
			conds = append(conds, resourceCond{field: w[:i], value: strings.Trim(w[i+1:], `"'`), sensitive: sens})
			continue
		}
		text = append(text, w)
	}
	return conds, strings.Join(text, " "), nil
}

// resourceFieldNames is every kind field name in this repo's lens, mapped
// to whether any kind marks it sensitive (OV34/OV39-SENSCOND: a name that
// is sensitive anywhere is printed masked).
func resourceFieldNames(ctx context.Context) (map[string]bool, error) {
	res, err := coreApp.SQL(ctx, opssql.Request{Query: `SELECT name, MAX(COALESCE(sensitive, 0)) FROM kind_fields_all GROUP BY name`, Timeout: opssql.DefaultTimeout})
	if err != nil {
		if errs.Has(err, errs.InvalidInput) {
			return map[string]bool{}, nil // no kinds table in this cache yet
		}
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range res.Rows {
		if len(r) > 1 {
			out[r[0].String()] = r[1].String() != "0" // MAX(COALESCE(sensitive, 0)): 0 or 1
		}
	}
	return out, nil
}

// sqlText is s as a SQL string literal.
func sqlText(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// searchResourceFields is the resources entry when the query has field
// conditions: resources of this repo whose fields equal every condition
// (case-insensitive), narrowed to the full-text hits of text when text is
// not empty. Each hit's snippet names the matched fields.
func searchResourceFields(ctx context.Context, eng *engine.Engine, t globalSearchTarget, conds []resourceCond, text string, sc globalSearchScope, perTable int) ([]globalSearchHit, error) {
	q := `SELECT r.id FROM resources_all r WHERE r.source = ` + sqlText(string(store.SourceRepo))
	var said []string
	for _, c := range conds {
		q += ` AND EXISTS (SELECT 1 FROM kind_fields_all f WHERE f.kind_id = r.kind AND f.name = ` + sqlText(c.field) +
			` AND lower(CAST(json_extract(r.fields, '$."' || f.id || '".v') AS TEXT)) = lower(` + sqlText(c.value) + `))`
		said = append(said, saidValue(c.field, c.value, c.sensitive))
	}
	q += ` ORDER BY r.name`
	// OV34/OV39-SENSCOND: match on the real value (unmasked lens); only the
	// id comes back and the snippet masks a sensitive term's value.
	res, err := coreApp.SQL(ctx, opssql.Request{Query: q, ShowSensitive: true, Timeout: opssql.DefaultTimeout})
	if err != nil {
		return nil, err
	}
	var only map[string]bool
	if text != "" {
		ft, err := coreApp.Search(ctx, t.table, quoteSearchTerms(text), searchFetch)
		if err != nil {
			return nil, err
		}
		only = map[string]bool{}
		for _, h := range ft {
			only[h.ID.String()] = true
		}
	}
	list, err := eng.List(ctx, store.SourceRepo, t.table)
	if err != nil {
		return nil, err
	}
	rows := make(map[string]row.Row, len(list))
	for _, r := range list {
		rows[r.ID.String()] = r
	}
	out := make([]globalSearchHit, 0, perTable)
	for _, cells := range res.Rows {
		if len(out) == perTable || len(cells) == 0 {
			break
		}
		id := cells[0].String()
		r, ok := rows[id]
		if !ok || !sc.keeps(t, r) || (only != nil && !only[id]) {
			continue
		}
		h := globalHitOf(t, id, "", r)
		h.Body = searchField(r, "name") + "  (field " + strings.Join(said, ", ") + ")"
		out = append(out, h)
	}
	return out, nil
}

// noMetaKeys is the key test of a text that holds no meta terms.
func noMetaKeys(string) bool { return false }

// metaSearchTerms splits query into metadata conditions (METADATA 3.17:
// key:value, key=value, key? whose key part is a metadata key name this
// repo's lens can see, from the registry or from the rows' own entries)
// and the remaining words. A query without ':', '=' or '?' is not looked
// at (no lens query).
//
// The returned set names every key some entry or the registry marks
// sensitive (OV34/OV39-SENSCOND: a condition on it matches the real value
// and its value is printed masked; see metaKeySensitive).
func metaSearchTerms(ctx context.Context, query string) ([]metaquery.Term, string, map[string]bool, error) {
	if !strings.ContainsAny(query, ":=?<>~!") { // OV39-CMP: comparisons too
		return nil, query, nil, nil
	}
	known := map[string]bool{}
	sensitive := map[string]bool{}
	for _, q := range []string{
		`SELECT key_name, MAX(COALESCE(sensitive, 0)) FROM row_meta_all WHERE item_idx = -1 GROUP BY key_name`,
		`SELECT name, MAX(COALESCE(sensitive, 0)) FROM meta_keys_all GROUP BY name`,
	} {
		res, err := coreApp.SQL(ctx, opssql.Request{Query: q, Timeout: opssql.DefaultTimeout})
		if err != nil {
			if errs.Has(err, errs.InvalidInput) {
				continue // no such view in this cache yet
			}
			return nil, "", nil, err
		}
		for _, r := range res.Rows {
			if len(r) > 1 {
				known[r[0].String()] = true
				if r[1].String() != "0" { // MAX(COALESCE(sensitive, 0)): 0 or 1
					sensitive[r[0].String()] = true
				}
			}
		}
	}
	var conds []metaquery.Term
	var words []string
	for _, t := range metaquery.Parse(query, func(n string) bool { return known[n] }) {
		if t.Kind == metaquery.Text {
			words = append(words, t.Word)
			continue
		}
		conds = append(conds, t)
	}
	return conds, strings.Join(words, " "), sensitive, nil
}

// metaKeySensitive tells whether a meta term's name (a key, or key.path
// for an object field) is in sensitive, by itself or by its top-level key
// (OV34/OV39-SENSCOND; fail closed: either marks it).
func metaKeySensitive(name string, sensitive map[string]bool) bool {
	if sensitive[name] {
		return true
	}
	top, _, ok := strings.Cut(name, ".")
	return ok && sensitive[top]
}

// searchMetaTable is one table's entry when the query has metadata
// conditions: rows of this repo matching every condition exactly through
// row_meta (case-insensitive text equality; an allowed value by its name),
// narrowed to the full-text hits of ftsText when it is not empty. A
// condition on a sensitive key matches the real value (unmasked lens) and
// its value is printed masked (OV34/OV39-SENSCOND); key? is unchanged.
func searchMetaTable(ctx context.Context, eng *engine.Engine, t globalSearchTarget, conds []metaquery.Term, sensitive map[string]bool, ftsText string, sc globalSearchScope, perTable int) ([]globalSearchHit, error) {
	q := `SELECT DISTINCT r.entity_id FROM row_meta_all r WHERE r.source = ` + sqlText(string(store.SourceRepo)) +
		` AND r.entity_table = ` + sqlText(t.table.String()) + ` AND r.item_idx = -1`
	var said []string
	for _, c := range conds {
		// P2-META-9: repo:<name>:… names a repo; search reads only this
		// repo's rows, so another repo's name matches nothing here.
		if c.Level == metaquery.LevelRepo && c.Place != "" {
			if dir, derr := coreApp.RepoDirName(ctx); derr != nil || dir != c.Place {
				return nil, nil
			}
		}
	}
	for _, c := range conds {
		// METADATA 3.17 / OV39-CMP: the row_meta condition comes from the
		// cache adapter (key:value, key?, > >= < <= ~= !=, scope prefixes).
		cc := c
		exists := "EXISTS"
		if c.Kind == metaquery.Cmp && c.Op == meta.OpNe {
			// != : the row has the key and no leaf equals the value
			cc.Kind, exists = metaquery.Cond, "NOT EXISTS"
			q += ` AND EXISTS (SELECT 1 FROM row_meta_all h WHERE h.source = r.source AND h.entity_table = r.entity_table` +
				` AND h.entity_id = r.entity_id AND h.key_name = ` + sqlText(c.Name()) + ` AND h.item_idx = -1)`
		}
		match, err := sqlite.RowMetaCond("x", cc)
		if err != nil {
			return nil, err
		}
		switch c.Kind {
		case metaquery.Has:
			said = append(said, c.Name()+"?")
		case metaquery.Cmp:
			v := c.Value
			if metaKeySensitive(c.Name(), sensitive) {
				v = searchMask
			}
			said = append(said, c.Name()+" "+string(c.Op)+" "+v)
		default:
			said = append(said, saidValue(c.Name(), c.Value, metaKeySensitive(c.Name(), sensitive)))
		}
		q += ` AND ` + exists + ` (SELECT 1 FROM row_meta_all x WHERE x.source = r.source AND x.entity_table = r.entity_table` +
			` AND x.entity_id = r.entity_id AND x.key_name = ` + sqlText(c.Name()) + ` AND ` + match + `)`
	}
	q += ` ORDER BY r.entity_id`
	// OV34/OV39-SENSCOND: match on the real value; only ids come back and
	// the snippet masks a sensitive key's value.
	res, err := coreApp.SQL(ctx, opssql.Request{Query: q, ShowSensitive: true, Timeout: opssql.DefaultTimeout})
	if err != nil {
		if errs.Has(err, errs.InvalidInput) {
			return nil, nil // no row_meta view in this cache yet
		}
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, nil
	}
	var only map[string]bool
	if ftsText != "" {
		ft, err := coreApp.Search(ctx, t.table, ftsText, searchFetch)
		if err != nil {
			return nil, err
		}
		only = map[string]bool{}
		for _, h := range ft {
			only[h.ID.String()] = true
		}
	}
	list, err := eng.List(ctx, store.SourceRepo, t.table)
	if err != nil {
		return nil, err
	}
	rows := make(map[string]row.Row, len(list))
	for _, r := range list {
		rows[r.ID.String()] = r
	}
	out := make([]globalSearchHit, 0, perTable)
	for _, cells := range res.Rows {
		if len(out) == perTable || len(cells) == 0 {
			break
		}
		id := cells[0].String()
		r, ok := rows[id]
		if !ok || !sc.keeps(t, r) || (only != nil && !only[id]) {
			continue
		}
		h := globalHitOf(t, id, "", r)
		label := h.Body
		if label == "" {
			label = h.fallback
		}
		h.Body = strings.TrimSpace(label + "  (" + strings.Join(said, ", ") + ")")
		out = append(out, h)
	}
	return out, nil
}
