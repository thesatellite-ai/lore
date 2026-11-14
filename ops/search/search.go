// Package search defines the top-level `lore search` noun (layer L5):
// search.run (the cross-table full-text fan-out, CLI `lore search "<q>"`),
// search.status (the cache's full-text index per table) and search.rebuild
// (rebuild the stores' caches from their row files).
//
// Ported from the old binary's search_global.go / search_admin.go (PASS3
// ONE-PATH, P3-MISC-1): the ranking, scope filters, metadata and resource
// field conditions and the trash tail run in wire over the core caches
// (Env); this package checks the input, holds the searched table list and
// prints the v0.1.17 text and --json envelope.
//
//	query ─▶ Env.GlobalSearch (per table: meta conds | resource field conds | FTS5)
//	      ─▶ RRF merge (score = 1/(60+rank)) ─▶ cut to --limit ─▶ trash tail (--include-deleted)
//	      ─▶ Text: "Found N hits for "q" across M entity types:" + one line per hit
package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/logic/metaquery"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (`lore search run|status|rebuild`; bare `lore search
// "<q>"` runs search.run through the CLI shortcut).
const (
	Noun      = "search"
	OpRun     = "search.run"
	OpStatus  = "search.status"
	OpRebuild = "search.rebuild"
)

// restNoun keeps the REST collection "search" (the default plural would be
// "searches").
const restNoun = "search"

// JSON envelope kinds and version (v0.1.17 constants.KindSearchGlobal /
// KindSearchStatus, Round 25 #3 stable contract: {schema_version, kind,
// count, data}).
const (
	KindGlobal    = "search.global"
	KindStatus    = "search.status"
	SchemaVersion = 1
)

// Defaults and tuning (v0.1.17 values).
// ds:def id=search-defaults-z8rpj5sv owner=@khanakia stability=stable desc="lore search defaults (--limit 20, --per-table 10)"
const (
	DefaultLimit    = 20
	DefaultPerTable = 10
	// RRFK is the standard Reciprocal Rank Fusion constant.
	RRFK = 60.0
	// BodyMax is the label length of a body-labelled hit (truncBody).
	BodyMax = 80
	// lineMax caps one printed hit text (v0.1.17: 100, cut to 97 + "...").
	lineMax = 100
)

// Label says which row field labels a hit and whether it is cut to BodyMax.
type Label struct {
	Field string // "" = no Body (memory: v0.1.17 printed only the id)
	Trunc bool
}

// Target is one table `lore search` fans out over. The scope flags mirror
// v0.1.17's fts5 helpers (entityHasProjectID / RepoID / ArchivedAt): a
// filter applies only where v0.1.17 applied it, so the same rows match.
type Target struct {
	Entity      string // the --tables name and the "entity" json key
	Table       row.Table
	Label       Label
	HasProject  bool
	HasRepo     bool
	HasArchived bool
}

// EntityResource is the resources entry (field:value conditions, RES 3.12).
const EntityResource = "resource"

// Targets is every searched table, sorted by entity (the v0.1.17 registry
// order, which also orders ties). A fresh slice each call.
func Targets() []Target {
	return []Target{
		{Entity: "architecturenote", Table: schema.TableArchitectureNotes, Label: Label{Field: "title"}, HasProject: true, HasRepo: true},
		{Entity: "behaviour", Table: schema.TableBehaviours, Label: Label{Field: "name"}, HasProject: true},
		{Entity: "comment", Table: schema.TableComments, Label: Label{Field: "body", Trunc: true}},
		{Entity: "cookbookrecipe", Table: schema.TableCookbookRecipes, Label: Label{Field: "title"}, HasProject: true},
		{Entity: "decision", Table: schema.TableDecisions, Label: Label{Field: "title"}, HasProject: true, HasRepo: true, HasArchived: true},
		{Entity: "handoff", Table: schema.TableHandoffs, Label: Label{Field: "body", Trunc: true}, HasProject: true},
		{Entity: "hotfix", Table: schema.TableHotfixes, Label: Label{Field: "title"}, HasProject: true, HasRepo: true, HasArchived: true},
		{Entity: "incident", Table: schema.TableIncidents, Label: Label{Field: "title"}, HasProject: true},
		{Entity: "memory", Table: schema.TableMemories, HasProject: true, HasRepo: true, HasArchived: true},
		{Entity: "mission", Table: schema.TableMissions, Label: Label{Field: "title"}, HasProject: true},
		{Entity: "pattern", Table: schema.TablePatterns, Label: Label{Field: "title"}, HasProject: true, HasRepo: true, HasArchived: true},
		{Entity: "plan", Table: schema.TablePlans, Label: Label{Field: "title"}, HasProject: true},
		{Entity: "playbook", Table: schema.TablePlaybooks, Label: Label{Field: "name"}, HasProject: true, HasArchived: true},
		{Entity: "prompt", Table: schema.TablePrompts, Label: Label{Field: "name"}, HasProject: true, HasArchived: true},
		// resources (RES 3.12, R8): name, kind name and non-sensitive field values
		{Entity: EntityResource, Table: schema.TableResources, Label: Label{Field: "name"}, HasArchived: true},
		{Entity: "rule", Table: schema.TableRules, Label: Label{Field: "body", Trunc: true}, HasProject: true, HasRepo: true, HasArchived: true},
		{Entity: "snapshot", Table: schema.TableSnapshots, Label: Label{Field: "title"}, HasProject: true, HasArchived: true},
		{Entity: "suggestion", Table: schema.TableSuggestions, Label: Label{Field: "title"}, HasProject: true},
		{Entity: "task", Table: schema.TableTasks, Label: Label{Field: "title"}, HasProject: true, HasRepo: true},
		{Entity: "tasklist", Table: schema.TableTasklists, Label: Label{Field: "title"}, HasProject: true},
		{Entity: "tastepref", Table: schema.TableTastePrefs, Label: Label{Field: "body", Trunc: true}, HasProject: true},
		{Entity: "techdoc", Table: schema.TableTechDocs, Label: Label{Field: "name"}, HasProject: true},
		{Entity: "workflow", Table: schema.TableWorkflows, Label: Label{Field: "name"}, HasProject: true},
		{Entity: "workspace", Table: schema.TableWorkspaces, Label: Label{Field: "name"}, HasProject: true},
	}
}

// TargetsFor narrows Targets to the --tables entity names (comma lists
// split); none matching is E_INVALID_INPUT, as v0.1.17 refused it.
func TargetsFor(tables []string) ([]Target, error) {
	all := Targets()
	wanted := map[string]bool{}
	for _, t := range tables {
		for _, w := range strings.Split(t, ",") {
			if w = strings.TrimSpace(w); w != "" {
				wanted[w] = true
			}
		}
	}
	if len(wanted) == 0 {
		return all, nil
	}
	out := make([]Target, 0, len(wanted))
	for _, t := range all {
		if wanted[t.Entity] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		given := make([]string, 0, len(wanted))
		for w := range wanted {
			given = append(given, w)
		}
		sort.Strings(given)
		known := make([]string, len(all))
		for i, t := range all {
			known[i] = t.Entity
		}
		return nil, errs.New(errs.InvalidInput, "none of --tables matched a known entity: "+strings.Join(given, ", ")).
			WithHint("give one or more of: " + strings.Join(known, ", "))
	}
	return out, nil
}

// Request is one search as search.run hands it to Env.
type Request struct {
	Query           string
	Targets         []Target
	Limit, PerTable int // > 0 here
	IncludeArchived bool
	IncludeDeleted  bool
	AllRepos        bool
	MasterOnly      bool
	// Repo is --repo (mount name or rep_ id); "" = the default scope. The
	// project is always the repo's one project (CLI_REFERENCE 2.5 lists no
	// --project; P3-MISC-1).
	Repo string
	// Store is --store (alias --to): only hits of that attached store; ""
	// = every store. An unattached name is E_NOT_FOUND (wire).
	Store string
}

// Hit is one cross-table match, the v0.1.17 globalSearchHit JSON shape.
type Hit struct {
	EntityTable string  `json:"entity_table"`
	Entity      string  `json:"entity" desc:"singular noun (memory, rule, …)"`
	ID          string  `json:"id"`
	Pretty      string  `json:"pretty,omitempty"`
	Score       float64 `json:"score"`
	Snippet     string  `json:"snippet,omitempty" desc:"the matched excerpt, matches in <b>…</b>"`
	Body        string  `json:"body,omitempty" desc:"short label of the row (title, name or cut body)"`
	// Source is the `_source` of a hit from an attached store (FLAG1);
	// zero for a repo hit, so repo hits' bytes stay v0.1.17's.
	Source store.SourceRef `json:"_source,omitzero"`
	// InTrash marks a row in the trash, listed only with --include-deleted
	// after the live hits (DELETION 2.9, FA-META-4).
	InTrash bool `json:"in_trash,omitempty"`
	// Meta / MetaMasked are the hit row's metadata by name, sensitive
	// values masked (MQ13 / METADATA 3.17: search hits carry it; absent
	// when the row has none, so v0.1.17's hit bytes stay for such rows).
	Meta       map[string]string `json:"meta,omitempty"`
	MetaMasked []string          `json:"meta_masked,omitempty"`
	// Fallback is the terminal text when neither Snippet nor Body is set;
	// never in --json, so the v0.1.17 envelope is unchanged.
	Fallback string `json:"-"`
}

// Result is what Env.GlobalSearch found: the merged, cut hits (trash tail
// included) and how many tables had a live hit.
type Result struct {
	Hits          []Hit
	MatchedTables int
}

// TableStatus is one table of search.status: the rows of the table in the
// caches and the rows in its full-text index.
type TableStatus struct {
	Entity     string `json:"entity"`
	Table      string `json:"table"`
	FTSTable   string `json:"fts_table"`
	FTSRows    int    `json:"fts_rows"`
	SourceRows int    `json:"source_rows"`
	Drift      int    `json:"drift"`
	OK         bool   `json:"ok"`
}

// Env is what the search operations need; *wire.App implements it.
//
// Invariant: GlobalSearch only reads (the caches and the engine's rows);
// RebuildSearch rebuilds every attached store's cache from its row files
// and changes no row.
type Env interface {
	GlobalSearch(ctx context.Context, r Request) (Result, error)
	SearchStatus(ctx context.Context, targets []Target) ([]TableStatus, error)
	// RebuildSearch returns the names of the stores whose cache it rebuilt.
	RebuildSearch(ctx context.Context) ([]string, error)
}

// RunIn is the input of search.run.
type RunIn struct {
	Query           string   `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT, -word leaves out rows holding word (a '-' inside a word like well-known is no negation); key:value matches metadata, field:value resource fields. Chinese, Japanese and Korean words are found inside a sentence (no spaces needed); emoji are found too (👍 also finds 👍🏽)"`
	Tables          []string `json:"tables,omitempty" flag:"tables" desc:"restrict to these entity types (e.g. memory,rule,decision; repeatable)"`
	Limit           int      `json:"limit,omitempty" flag:"limit" desc:"max total hits after merge (default 20)"`
	PerTable        int      `json:"per_table,omitempty" flag:"per-table" desc:"per-entity top-K before merge (default 10)"`
	IncludeArchived bool     `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows"`
	IncludeDeleted  bool     `json:"include_deleted,omitempty" flag:"include-deleted" desc:"also list matching rows in the trash, after the live hits"`
	AllRepos        bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"search every repo (ignore --repo)"`
	MasterOnly      bool     `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows"`
	NoInherit       bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope (reserved)"`
	Repo            string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	Store           string   `json:"store,omitempty" flag:"store" alias:"to" desc:"only hits from this store (a name from .lore/stores.yaml, me, or repo; default every attached store)"`
}

// RunData is the data of the search.global envelope.
type RunData struct {
	Query string `json:"query"`
	Count int    `json:"count"`
	Hits  []Hit  `json:"hits"`
	// External is v0.1.17's list of rows outside the checkout. Those rows
	// now come from the stores the core attaches, as hits with a _source
	// (P3-MISC-1), so it is always empty; the key stays for scripts.
	External []Hit `json:"external"`
}

// RunOut is the output of search.run: the v0.1.17 --json envelope.
type RunOut struct {
	SchemaVersion int     `json:"schema_version"`
	Kind          string  `json:"kind"`
	Count         int     `json:"count,omitempty"`
	Data          RunData `json:"data"`
	// matched is the number of tables with a hit, for the header line.
	matched int
}

// StatusIn is the input of search.status.
type StatusIn struct {
	Tables []string `json:"tables,omitempty" flag:"tables" desc:"only these entity types (e.g. memory,rule)"`
}

// StatusData is the data of the search.status envelope.
type StatusData struct {
	FTS5Available bool          `json:"fts5_available"`
	DriftDetected bool          `json:"drift_detected"`
	Entities      []TableStatus `json:"entities"`
}

// StatusOut is the output of search.status: the v0.1.17 envelope.
type StatusOut struct {
	SchemaVersion int        `json:"schema_version"`
	Kind          string     `json:"kind"`
	Count         int        `json:"count,omitempty"`
	Data          StatusData `json:"data"`
}

// RebuildIn is the input of search.rebuild.
type RebuildIn struct {
	// Kind is accepted for v0.1.17 scripts: the core cache is rebuilt per
	// store, so every table is rebuilt (P3-MISC-1); an unknown name is
	// still refused.
	Kind string `json:"kind,omitempty" flag:"kind" desc:"v0.1.17: rebuild only this entity; the core rebuilds every table of each store's cache (an unknown name is refused)"`
}

// RebuildOut is the output of search.rebuild.
type RebuildOut struct {
	Stores []string `json:"stores"`
	Kind   string   `json:"kind,omitempty"`
}

// Ops returns the search operations bound to env.
func Ops(env Env) []op.Entry {
	readErrs := []errs.Code{
		errs.InvalidInput, errs.NotFound, errs.AmbiguousProject, errs.NotProjectRoot, errs.DBNotFound,
		errs.NotAStore, errs.StoreUnreachable, errs.OldLore, errs.Internal,
	}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Unified full-text search across all entities (fans out per entity + RRF-merges)",
			Permission: op.PermRead, Access: op.AccessRead, RESTNoun: restNoun, Errors: readErrs,
			Examples: []op.Example{
				{Title: "search every kind", CLI: `lore search --query "invoice rounding"`, Want: "hit"},
				{Title: "deploy, but not the rows that mention friday", CLI: `lore search --query "deploy -friday"`, Want: "hit"},
				{Title: "only rules and memories", CLI: `lore search --query deploy --tables rule,memory --json`, Want: "search.global"},
			},
		}, func(ctx context.Context, in RunIn) (RunOut, error) { return run(ctx, env, in) }),
		op.Define(op.Spec{
			Name: OpStatus, Summary: "Per-entity full-text index row counts and drift of the core cache",
			Permission: op.PermRead, Access: op.AccessRead, RESTNoun: restNoun, Errors: readErrs,
			Examples: []op.Example{{Title: "index health", CLI: "lore search status", Want: "ENTITY"}},
		}, func(ctx context.Context, in StatusIn) (StatusOut, error) { return status(ctx, env, in) }),
		op.Define(op.Spec{
			Name: OpRebuild, Summary: "Rebuild the search index: every attached store's cache, from its row files",
			Permission: op.PermRead, Access: op.AccessRead, RESTNoun: restNoun, Errors: readErrs,
			Examples: []op.Example{{Title: "reindex", CLI: "lore search rebuild", Want: "rebuilt"}},
		}, func(ctx context.Context, in RebuildIn) (RebuildOut, error) { return rebuild(ctx, env, in) }),
	}
}

// run checks the input and asks env; a blank query is refused as v0.1.17
// did (FTS5 cannot match "nothing").
func run(ctx context.Context, env Env, in RunIn) (RunOut, error) {
	if strings.TrimSpace(in.Query) == "" {
		return RunOut{}, errs.New(errs.InvalidInput, "search needs a query").
			WithHint("use `lore <kind> list` (same --repo / --all-repos / --no-inherit flags) to see every row in scope")
	}
	if metaquery.OnlyNegated(in.Query) {
		// FTS5's NOT needs something to subtract from: "-friday" alone
		// would mean every row in scope without friday
		return RunOut{}, errs.New(errs.InvalidInput, "search needs a word to search for: every term of "+fmt.Sprintf("%q", in.Query)+" starts with '-', which leaves rows out").
			WithHint(`add a word to search for: lore search --query "deploy -friday"`)
	}
	targets, err := TargetsFor(in.Tables)
	if err != nil {
		return RunOut{}, err
	}
	if in.Limit <= 0 {
		in.Limit = DefaultLimit
	}
	if in.PerTable <= 0 {
		in.PerTable = DefaultPerTable
	}
	res, err := env.GlobalSearch(ctx, Request{
		Query: in.Query, Targets: targets, Limit: in.Limit, PerTable: in.PerTable,
		IncludeArchived: in.IncludeArchived, IncludeDeleted: in.IncludeDeleted,
		AllRepos: in.AllRepos, MasterOnly: in.MasterOnly, Repo: in.Repo, Store: in.Store,
	})
	if err != nil {
		return RunOut{}, err
	}
	hits := res.Hits
	if hits == nil {
		hits = []Hit{}
	}
	if me, ok := env.(metashow.Env); ok {
		for i := range hits {
			if hits[i].InTrash {
				continue // a trashed row is read only by the trash commands
			}
			b := metashow.Of(ctx, me, hits[i].ID)
			hits[i].Meta, hits[i].MetaMasked = b.Meta, b.MetaMasked
		}
	}
	return RunOut{
		SchemaVersion: SchemaVersion, Kind: KindGlobal, Count: len(hits),
		Data:    RunData{Query: in.Query, Count: len(hits), Hits: hits, External: []Hit{}},
		matched: res.MatchedTables,
	}, nil
}

// Text is the terminal form: a header, then one line per hit, the type and
// id in columns as wide as the widest of this result (no padding inside
// brackets, which read as a broken label):
//
//	decision  dec_7a67…9504  We use Stripe for payments  (score=0.017)
//	memory    mem_2ad3…f891  Old payments page is gone  (score=0.017)
//
// --json is unchanged.
func (o RunOut) Text() string {
	if len(o.Data.Hits) == 0 {
		return "(no matches)\n"
	}
	var b strings.Builder
	n := len(o.Data.Hits)
	// matched counts the tables with a live FTS hit; the trashed rows of
	// --include-deleted and the external hits are not among them, so the
	// listed hits' own tables are counted too (never "across 0").
	seen := map[string]bool{}
	for _, h := range o.Data.Hits {
		seen[h.EntityTable] = true
	}
	types := max(o.matched, len(seen))
	fmt.Fprintf(&b, "Found %d %s for %q across %d %s:\n\n", n, plural(n, "hit", "hits"), o.Data.Query,
		types, plural(types, "entity type", "entity types"))
	typeW, idW := 0, 0
	for _, h := range o.Data.Hits {
		typeW = max(typeW, utf8.RuneCountInString(h.Entity))
		idW = max(idW, utf8.RuneCountInString(hitID(h)))
	}
	for _, h := range o.Data.Hits {
		pretty := hitID(h)
		body := PlainSnippet(h.Snippet)
		if body == "" {
			body = h.Body
		}
		if body == "" {
			body = h.Fallback
		}
		body = textcut.Cut(body, lineMax, lineMax-len(textcut.Ellipsis))
		if h.Source.Store != "" {
			body += "  " + StoreLabel(h.Source)
		}
		if h.InTrash {
			body += "  (in the trash)"
		}
		fmt.Fprintf(&b, "%s  %s  %s  (score=%.3f)\n", padRight(h.Entity, typeW), padRight(pretty, idW), body, h.Score)
	}
	return b.String()
}

// hitID is the id a hit shows: its short form, else the full id.
func hitID(h Hit) string {
	if h.Pretty != "" {
		return h.Pretty
	}
	return h.ID
}

// padRight pads s with spaces to w characters; the short ids hold "…", so
// the width counts characters, not bytes.
func padRight(s string, w int) string {
	if n := utf8.RuneCountInString(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// StoreLabel is the bracketed store label of a store hit ("[agency]",
// "[agency/acme-hub]").
func StoreLabel(src store.SourceRef) string {
	if src.Folder != "" {
		return "[" + src.Store + "/" + src.Folder + "]"
	}
	return "[" + src.Store + "]"
}

// snippetMarks are the match markers FTS5 snippet() wraps hits in; they
// stay in --json and are removed for the terminal.
var snippetMarks = strings.NewReplacer("<b>", "", "</b>", "")

// PlainSnippet is a snippet as the terminal shows it.
func PlainSnippet(s string) string { return snippetMarks.Replace(s) }

// TruncBody cuts s to n bytes, the last three "..." (v0.1.17 truncBody),
// never inside a UTF-8 character (textcut.Cut).
func TruncBody(s string, n int) string {
	return textcut.Cut(s, n, n-len(textcut.Ellipsis))
}

// plural picks the word for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// status asks env for the per-table index counts and marks drift.
func status(ctx context.Context, env Env, in StatusIn) (StatusOut, error) {
	targets, err := TargetsFor(in.Tables)
	if err != nil {
		return StatusOut{}, err
	}
	rows, err := env.SearchStatus(ctx, targets)
	if err != nil {
		return StatusOut{}, err
	}
	if rows == nil {
		rows = []TableStatus{}
	}
	drift := false
	for i := range rows {
		rows[i].Drift = rows[i].SourceRows - rows[i].FTSRows
		rows[i].OK = rows[i].Drift == 0
		drift = drift || !rows[i].OK
	}
	// FTS5 is built into the pure-Go SQLite driver (CLAUDE.md convention 6),
	// so the v0.1.17 "degraded to LIKE" branch cannot happen any more.
	return StatusOut{SchemaVersion: SchemaVersion, Kind: KindStatus, Count: len(rows),
		Data: StatusData{FTS5Available: true, DriftDetected: drift, Entities: rows}}, nil
}

// Text is v0.1.17's status table.
func (o StatusOut) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %-22s %8s %8s %s\n", "ENTITY", "FTS_TABLE", "FTS", "SOURCE", "STATUS")
	for _, r := range o.Data.Entities {
		st := "✓ ok"
		if r.Drift != 0 {
			st = fmt.Sprintf("⚠ %d behind", r.Drift)
		}
		fmt.Fprintf(&b, "%-22s %-22s %8d %8d %s\n", r.Entity, r.FTSTable, r.FTSRows, r.SourceRows, st)
	}
	if o.Data.DriftDetected {
		b.WriteString("\nhint: run `lore search rebuild` to reindex\n")
	}
	return b.String()
}

// rebuild validates --kind and asks env to rebuild every store's cache.
func rebuild(ctx context.Context, env Env, in RebuildIn) (RebuildOut, error) {
	if in.Kind != "" {
		if _, err := TargetsFor([]string{in.Kind}); err != nil {
			return RebuildOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("unknown entity %q", in.Kind)).
				WithHint("run `lore search status` to see the list")
		}
	}
	stores, err := env.RebuildSearch(ctx)
	if err != nil {
		return RebuildOut{}, err
	}
	if stores == nil {
		stores = []string{}
	}
	return RebuildOut{Stores: stores, Kind: in.Kind}, nil
}

// Text is one line per rebuilt store.
func (o RebuildOut) Text() string {
	var b strings.Builder
	if o.Kind != "" {
		b.WriteString("note: the core rebuilds a store's whole cache; --kind " + o.Kind + " included\n")
	}
	fmt.Fprintf(&b, "✓ rebuilt the search index of %d %s", len(o.Stores), plural(len(o.Stores), "store", "stores"))
	if len(o.Stores) > 0 {
		b.WriteString(" (" + strings.Join(o.Stores, ", ") + ")")
	}
	b.WriteString("\n")
	return b.String()
}
