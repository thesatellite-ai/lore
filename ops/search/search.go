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
//	query ─▶ Env.GlobalSearch (per table: meta conds | resource field conds | FTS5 over the topic words)
//	      ─▶ one relevance score across kinds (words held, then title, then bm25 rank; D-435/D-436)
//	      ─▶ cut to --limit ─▶ trash tail (--include-deleted)
//	      ─▶ Text: "Found N hits for "q" across M entity types:" + one line per hit
package search

import (
	"cmp"
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/logic/factstale"
	"github.com/thesatellite-ai/lore/logic/metaquery"
	"github.com/thesatellite-ai/lore/logic/searchrank"
	"github.com/thesatellite-ai/lore/logic/staleclaim"
	"github.com/thesatellite-ai/lore/logic/textwidth"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/dateflag"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
	"github.com/thesatellite-ai/lore/ops/internal/morehits"
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
	// DefaultFullTop is how many of the best hits --full prints whole
	// (devco D-800, owner P-17): every hit's body made `search --full`
	// read more text than grep on the same question, so the hits past
	// the top keep their snippet unless --full-top asks for more.
	DefaultFullTop = 3
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
	// Roster marks the org's people and teams (devco D-489): they live in
	// the org roster, not in a store's cache, so wire matches them by
	// name, handle and email instead of full text, search status lists
	// no index for them, and --store / --repo (cache scopes) leave them out.
	Roster bool
}

// EntityMember / EntityTeam are the roster entries of Targets.
const (
	EntityMember = "member"
	EntityTeam   = "team"
)

// EntityResource is the resources entry (field:value conditions, RES 3.12).
const EntityResource = "resource"

// Targets is every searched table, sorted by entity (the v0.1.17 registry
// order, which also orders ties). A fresh slice each call.
func Targets() []Target {
	return []Target{
		{Entity: "architecturenote", Table: schema.TableArchitectureNotes, Label: Label{Field: "title"}, HasProject: true, HasRepo: true, HasArchived: true},
		{Entity: "behaviour", Table: schema.TableBehaviours, Label: Label{Field: "name"}, HasProject: true, HasArchived: true},
		{Entity: "comment", Table: schema.TableComments, Label: Label{Field: "body", Trunc: true}},
		{Entity: "cookbookrecipe", Table: schema.TableCookbookRecipes, Label: Label{Field: "title"}, HasProject: true, HasArchived: true},
		{Entity: "decision", Table: schema.TableDecisions, Label: Label{Field: "title"}, HasProject: true, HasRepo: true, HasArchived: true},
		{Entity: "handoff", Table: schema.TableHandoffs, Label: Label{Field: "body", Trunc: true}, HasProject: true},
		{Entity: "hotfix", Table: schema.TableHotfixes, Label: Label{Field: "title"}, HasProject: true, HasRepo: true, HasArchived: true},
		{Entity: "incident", Table: schema.TableIncidents, Label: Label{Field: "title"}, HasProject: true, HasArchived: true},
		{Entity: "memory", Table: schema.TableMemories, HasProject: true, HasRepo: true, HasArchived: true},
		// the org's people (name, handle, email) and teams (handle, name), D-489
		{Entity: EntityMember, Table: schema.TableActors, Label: Label{Field: "display_name"}, Roster: true},
		{Entity: "mission", Table: schema.TableMissions, Label: Label{Field: "title"}, HasProject: true},
		{Entity: "pattern", Table: schema.TablePatterns, Label: Label{Field: "title"}, HasProject: true, HasRepo: true, HasArchived: true},
		{Entity: "plan", Table: schema.TablePlans, Label: Label{Field: "title"}, HasProject: true},
		{Entity: "playbook", Table: schema.TablePlaybooks, Label: Label{Field: "name"}, HasProject: true, HasArchived: true},
		{Entity: "prompt", Table: schema.TablePrompts, Label: Label{Field: "name"}, HasProject: true, HasArchived: true},
		// resources (RES 3.12, R8): name, kind name and non-sensitive field values
		{Entity: EntityResource, Table: schema.TableResources, Label: Label{Field: "name"}, HasArchived: true},
		{Entity: "rule", Table: schema.TableRules, Label: Label{Field: "body", Trunc: true}, HasProject: true, HasRepo: true, HasArchived: true},
		{Entity: "snapshot", Table: schema.TableSnapshots, Label: Label{Field: "title"}, HasProject: true, HasArchived: true},
		{Entity: "suggestion", Table: schema.TableSuggestions, Label: Label{Field: "title"}, HasProject: true, HasArchived: true},
		{Entity: "task", Table: schema.TableTasks, Label: Label{Field: "title"}, HasProject: true, HasRepo: true},
		{Entity: "tasklist", Table: schema.TableTasklists, Label: Label{Field: "title"}, HasProject: true},
		{Entity: "tastepref", Table: schema.TableTastePrefs, Label: Label{Field: "body", Trunc: true}, HasProject: true, HasArchived: true},
		{Entity: EntityTeam, Table: schema.TableTeams, Label: Label{Field: "display_name"}, Roster: true},
		{Entity: "techdoc", Table: schema.TableTechDocs, Label: Label{Field: "name"}, HasProject: true, HasArchived: true},
		{Entity: "workflow", Table: schema.TableWorkflows, Label: Label{Field: "name"}, HasProject: true, HasArchived: true},
		{Entity: "workspace", Table: schema.TableWorkspaces, Label: Label{Field: "name"}, HasProject: true, HasArchived: true},
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
	// BornBranch is --born-branch (D-392): only rows created on that code
	// branch (their `_o.git.branch`); "" = every row.
	BornBranch string
	// Full is --full (D-758): every hit carries the row's whole text.
	Full bool
	// Window is --since / --until (P-9): only rows whose date
	// (schema.RowDate: valid_at, else created_at) falls in it; zero = all.
	Window dates.Window
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
	// Title is the row's title or name (D-436), "" for a kind labelled by
	// its body (rule, comment, handoff, tastepref) or by nothing (memory):
	// the terminal shows it before the snippet, so a playbook found in its
	// body still reads as its name.
	Title string `json:"title,omitempty" desc:"the row's title or name; empty for a kind without one"`
	// ResourceKind is a resource hit's kind name (D-436), so a hit on a
	// field value says what it is.
	ResourceKind string `json:"resource_kind,omitempty" desc:"a resource hit's kind name"`
	// Partial marks a hit holding only some of the query's words (D-435):
	// a natural-language query lists rows with every word first, then the
	// rows with most of them.
	Partial bool `json:"partial,omitempty" desc:"the row holds only some of the query's words"`
	// Source is the `_source` of a hit from an attached store (FLAG1);
	// zero for a repo hit, so repo hits' bytes stay v0.1.17's.
	Source store.SourceRef `json:"_source,omitzero"`
	// InTrash marks a row in the trash, listed only with --include-deleted
	// after the live hits (DELETION 2.9, FA-META-4).
	InTrash bool `json:"in_trash,omitempty"`
	// History is set on a row that is no longer the current answer
	// (schema.HistoryNote, devco D-820): superseded by another row or past
	// its valid_until. Such hits are listed after every current hit and
	// marked, so an agent never reads an old fact as the answer.
	History string `json:"history,omitempty" desc:"set when the row is no longer the current answer, read it as history: superseded by <id> or valid until <day>; such hits come after the current ones"`
	// CommentOn is set on a comment hit (devco D-942): the row it is on.
	// A comment is history, so it is listed directly under that row,
	// never above it (GroupComments).
	CommentOn CommentOn `json:"comment_on,omitzero" desc:"set on a comment hit: the row it comments on; a comment is history and is listed directly under that row, never above it"`
	// ViaComment is set on a row listed only because a comment on it
	// matched (devco D-942): the comment's id; the comment follows it.
	ViaComment string `json:"via_comment,omitempty" desc:"set on a row listed only because a comment on it matched: that comment's id, listed under it"`
	// Meta / MetaMasked are the hit row's metadata by name, sensitive
	// values masked (MQ13 / METADATA 3.17: search hits carry it; absent
	// when the row has none, so v0.1.17's hit bytes stay for such rows).
	Meta       map[string]string `json:"meta,omitempty"`
	MetaMasked []string          `json:"meta_masked,omitempty"`
	// Stale is set on a row stating a fact about code whose evidence
	// (fact_evidence) changed in git after it was verified
	// (fact_verified_at, devco:D-941): it may no longer hold, so it ranks
	// below a fresh hit with the same match.
	Stale string `json:"stale,omitempty" desc:"set when the row states a fact whose evidence files (fact_evidence) changed in git after it was verified (fact_verified_at): stale: evidence changed since <day> (<files>); check it before relying on it, it ranks below a fresh row with the same match"`
	// Fact is the hit row's evidence claim (nil when it names none), read
	// by the wire layer to set Stale; never in --json.
	Fact *factstale.Claim `json:"-"`
	// ContradictedBy is set on a row whose planned / not-yet claim a newer
	// hit on the same subject states as done (devco:D-1027, bug hunt
	// H-289): that row's id; Stale then says so too.
	ContradictedBy string `json:"contradicted_by,omitempty" desc:"set when the row says something is planned or not done yet and a newer row on the same subject says it is done (shipped, released, live): that newer row's id; stale then reads stale: a newer row says it is done: <id> (<day>); read both with lore show --id before relying on either"`
	// Claim is the hit row's text and time as staleclaim reads them (nil
	// for a card, a comment or a row in the trash), read by the wire layer
	// to set ContradictedBy; never in --json.
	Claim *staleclaim.Doc `json:"-"`
	// Held marks which of a natural query's topic words the hit holds
	// (by searchrank.Words.Terms index) and NameHeld which of them its
	// name or handle holds (a card: Card); nil for a query of one word,
	// in FTS5 syntax or with conditions. Read by Rerank; never in --json.
	Held     []bool `json:"-"`
	NameHeld []bool `json:"-"`
	// rank is the score the hit is ordered by (Rerank, devco:D-1007),
	// set when ranked is; RankScore reads it.
	rank   float64
	ranked bool
	// follows is set on a card that comes after every knowledge row
	// (CardsFollow, devco:D-1034); Before reads it.
	follows bool
	// Text is the row's whole text (its body, else its longest searched
	// field other than the title), set only with --full (devco D-758,
	// owner P-8): the snippet stops after a few words, and reading a note
	// took a second command per hit.
	Text string `json:"text,omitempty" desc:"the row's whole text (body), only with --full"`
	// Fallback is the terminal text when neither Snippet nor Body is set;
	// never in --json, so the v0.1.17 envelope is unchanged.
	Fallback string `json:"-"`
}

// staleRank is the share of its score a stale hit ranks with
// (devco:D-941): below a fresh hit with the same match, above a much
// weaker one.
const staleRank = 0.9

// RankScore is the score a hit is ordered by: its score, cut for a stale
// fact (Hit.Stale), and, once Rerank weighed it, its common words
// counted by rarity when it is a stale fact or a card. The printed score
// stays the match's own.
func RankScore(h Hit) float64 {
	s := h.Score
	if h.ranked {
		s = h.rank
	}
	if h.Stale != "" {
		return s * staleRank
	}
	return s
}

// Card reports whether entity is a card kind (devco:D-1007): a person,
// a team or a resource, a row that says who or what something is rather
// than how it works. Its name and handle count in full; a word only its
// description or fields hold counts by its rarity (Rerank), and it ranks
// after a knowledge row of the same score (Before).
func Card(entity string) bool {
	return entity == EntityMember || entity == EntityTeam || entity == EntityResource
}

// Rerank weighs the hits of a natural query of n words for the merged
// order (devco:D-1007): a stale fact's words and a card's description
// words count by their rarity (share, searchrank.Share by Terms index),
// so neither outranks a fresh knowledge row on the question's common
// words; a card's name and handle words, and every word of a fresh
// knowledge row, count in full. Hits without Held keep their score.
func Rerank(hits []Hit, n int, share []float64) {
	for i := range hits {
		h := &hits[i]
		card := Card(h.Entity)
		if len(h.Held) != n || (!card && h.Stale == "") {
			continue
		}
		full := make([]bool, n)
		matched := 0
		for k, held := range h.Held {
			if held {
				matched++
			}
			full[k] = card && k < len(h.NameHeld) && h.NameHeld[k]
		}
		h.rank = searchrank.Weighted(h.Score, matched, n, searchrank.Weight(h.Held, full, share))
		h.ranked = true
	}
}

// CardsFollow marks the cards of a long natural question (n topic words,
// at least searchrank.LongQuestion) to follow every knowledge row
// (devco:D-1034, bug hunt H-299): a question about how something works
// is answered by notes, decisions and rules, and the org's people and
// teams each holding a word or two of it (a product name that is also a
// team handle, the company name in every email) filled the first
// results. A card keeps its place by score when the question asks about
// people (people: searchrank.AsksAboutPeople: who owns it, which team)
// or when its name or handle holds at least half the question's words
// (the question names it). Cards are never dropped, only listed later.
func CardsFollow(hits []Hit, n int, people bool) {
	if people || n < searchrank.LongQuestion {
		return
	}
	for i := range hits {
		h := &hits[i]
		if !Card(h.Entity) || len(h.Held) != n {
			continue
		}
		named := 0
		for k := range h.Held {
			if h.Held[k] && k < len(h.NameHeld) && h.NameHeld[k] {
				named++
			}
		}
		h.follows = 2*named < n
	}
}

// Before is the merged order of two current hits (devco D-436, D-941,
// D-1007, D-1034): a card that follows the knowledge rows (CardsFollow)
// after every hit that does not, then the higher RankScore first, and at
// an equal one a knowledge row before a card. -1 when a goes first, 1
// when b does, 0 to keep their order (kind order).
func Before(a, b Hit) int {
	if a.follows != b.follows {
		if a.follows {
			return 1
		}
		return -1
	}
	if c := cmp.Compare(RankScore(b), RankScore(a)); c != 0 {
		return c
	}
	if ca, cb := Card(a.Entity), Card(b.Entity); ca != cb {
		if cb {
			return -1
		}
		return 1
	}
	return 0
}

// Result is what Env.GlobalSearch found: the merged, cut hits (trash tail
// included) and how many tables had a live hit.
type Result struct {
	Hits          []Hit
	MatchedTables int
	// More is true when more rows match than Hits lists: a kind held more
	// than PerTable hits, or the merge held more than Limit (devco D-767).
	More bool
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
// RebuildSearch rebuilds the attached stores' caches from their row files
// and changes no row.
type Env interface {
	GlobalSearch(ctx context.Context, r Request) (Result, error)
	SearchStatus(ctx context.Context, targets []Target) ([]TableStatus, error)
	// RebuildSearch rebuilds the cache of every attached store, or only of
	// the store (and its folders) only names when it is not "" (an
	// unattached name is E_NOT_FOUND), and returns the names of the stores
	// whose cache it rebuilt.
	RebuildSearch(ctx context.Context, only string) ([]string, error)
}

// RunIn is the input of search.run.
type RunIn struct {
	Query           string   `json:"query" arg:"0" rest:"true" desc:"full-text query (several words without quotes are one query): plain words or a question (\"how do we deploy orbit\": words like how, do, we are skipped, word forms match, rows holding most words first), or FTS5 syntax that every hit must match: \"exact phrase\", prefix*, AND / OR / NOT, -word leaves out rows holding word (a '-' inside a word like well-known is no negation); key:value matches metadata, field:value resource fields. Chinese, Japanese and Korean words are found inside a sentence (no spaces needed); emoji are found too (👍 also finds 👍🏽)"`
	Tables          []string `json:"tables,omitempty" flag:"tables" desc:"restrict to these entity types (e.g. memory,rule,decision; repeatable)"`
	Limit           int      `json:"limit,omitempty" flag:"limit" min:"1" desc:"max total hits after merge (default 20); a cut list ends with a note and --json says \"more\": true"`
	PerTable        int      `json:"per_table,omitempty" flag:"per-table" min:"1" desc:"per-entity top-K before merge (default 10, or --limit when --limit is given)"`
	IncludeArchived bool     `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows"`
	IncludeDeleted  bool     `json:"include_deleted,omitempty" flag:"include-deleted" desc:"also list matching rows in the trash, after the live hits"`
	AllRepos        bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"search every repo (ignore --repo)"`
	MasterOnly      bool     `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows"`
	NoInherit       bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope (reserved)"`
	Repo            string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	Store           string   `json:"store,omitempty" flag:"store" alias:"to" desc:"only hits from this store (a name from .lore/stores.yaml, me, or repo; default every attached store; outside a code repo, where no setup covers the folder, your personal store)"`
	Full            bool     `json:"full,omitempty" flag:"full" desc:"print the whole text (body) of the best hits under their line instead of the snippet (the top 3, see --full-top); --json adds it as text on those hits"`
	// FullTop is --full-top (devco D-800): how many of the best hits
	// --full prints whole; 0 = DefaultFullTop. Giving it turns --full on.
	FullTop int `json:"full_top,omitempty" flag:"full-top" min:"1" desc:"with --full, how many of the best hits print their whole text (default 3; implies --full); the other hits keep their snippet"`
	// Born is the --born-branch flag every list shares (D-392).
	metalist.Born
	// Since / Until keep rows whose date falls in the window (P-9): the
	// day a memory is about (memory add --at), else when the row was
	// added; days inclusive.
	Since string `json:"since,omitempty" flag:"since" desc:"only rows about this day or later: a memory's valid_at (memory add --at), else when the row was added (YYYY-MM-DD or a date phrase)"`
	Until string `json:"until,omitempty" flag:"until" desc:"only rows about this day or earlier: valid_at, else when added (YYYY-MM-DD or a date phrase)"`
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
	// More says the hits are cut: more rows match than are listed (devco
	// D-767). "Found 10 hits" over fifty matching notes read as the whole
	// answer; --limit lists more.
	More bool `json:"more,omitempty" desc:"more rows match than are listed; raise --limit to list more"`
}

// RunOut is the output of search.run: the v0.1.17 --json envelope.
type RunOut struct {
	SchemaVersion int     `json:"schema_version"`
	Kind          string  `json:"kind"`
	Count         int     `json:"count,omitempty"`
	Data          RunData `json:"data"`
	// matched is the number of tables with a hit, for the header line.
	matched int
	// limit is the --limit the search ran with, for the cut note.
	limit int
	// fullTop / fullCapped are --full's cut (devco D-800): how many hits
	// print their whole text, and how many more had one and show only
	// their snippet, for the note that says how to print them.
	fullTop, fullCapped int
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
	// Store names the one store to rebuild, as search --store does; it
	// also lets the rebuild run outside a repo, on the store it names
	// (owner P-5, devco D-763: from the home folder the rebuild had no
	// store flag, so a personal store's cache could not be rebuilt there).
	Store string `json:"store,omitempty" flag:"store" alias:"to" desc:"rebuild only this store's cache (a name from .lore/stores.yaml, me, <store>/<folder>, or repo; default every attached store); works from any folder"`
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
			Name: OpRun, Summary: "Search every kind at once: a plain question finds the rows holding its words, best first",
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
			Examples: []op.Example{
				{Title: "reindex", CLI: "lore search rebuild", Want: "rebuilt"},
				{Title: "reindex one store, from any folder", CLI: "lore search rebuild --store me/notes", Want: "rebuilt"},
			},
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
	win, err := dateflag.Window(dateflag.LocalClock(env), in.Since, in.Until)
	if err != nil {
		return RunOut{}, err
	}
	if in.PerTable <= 0 {
		// D-767: --limit 50 alone means up to 50 hits; the per-kind cut
		// of 10 kept a search over fifty notes at 10 whatever --limit said
		in.PerTable = DefaultPerTable
		if in.Limit > 0 {
			in.PerTable = in.Limit
		}
	}
	if in.Limit <= 0 {
		in.Limit = DefaultLimit
	}
	if in.FullTop > 0 {
		in.Full = true // --full-top 5 alone means --full for the top 5
	}
	if in.FullTop <= 0 {
		in.FullTop = DefaultFullTop
	}
	res, err := env.GlobalSearch(ctx, Request{
		Query: in.Query, Targets: targets, Limit: in.Limit, PerTable: in.PerTable,
		IncludeArchived: in.IncludeArchived, IncludeDeleted: in.IncludeDeleted,
		AllRepos: in.AllRepos, MasterOnly: in.MasterOnly, Repo: in.Repo, Store: in.Store,
		BornBranch: in.BornBranch, Full: in.Full, Window: win,
	})
	if err != nil {
		return RunOut{}, err
	}
	hits := res.Hits
	if hits == nil {
		hits = []Hit{}
	}
	capped := 0
	for i := range hits {
		if hits[i].Text == "" {
			continue
		}
		switch {
		case !in.Full:
			hits[i].Text = "" // D-758: the whole text only on --full
		case i >= in.FullTop:
			// D-800: the whole text only for the best hits; the rest keep
			// their snippet (hits are best first, the trash after them)
			hits[i].Text = ""
			capped++
		}
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
		Data:    RunData{Query: in.Query, Count: len(hits), Hits: hits, External: []Hit{}, More: res.More},
		matched: res.MatchedTables, limit: in.Limit, fullTop: in.FullTop, fullCapped: capped,
	}, nil
}

// Text is the terminal form: a header, then one line per hit, the type and
// id in columns as wide as the widest of this result (no padding inside
// brackets, which read as a broken label), then the row's title (a
// resource's name and kind) and a snippet cut around the first matched
// word (D-436):
//
//	decision  dec_7a67…9504  We use Stripe for payments — …invoices go through Stripe since…  (score=0.983)
//	resource  res_2ad3…f891  web-01 (server) — 10.0.0.4 hetzner…  (score=0.650)
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
	lead := "Found"
	if o.Data.More {
		lead = "Showing the best" // D-767: not every match; the note at the end says how to list more
	}
	fmt.Fprintf(&b, "%s %d %s for %q across %d %s:\n\n", lead, n, plural(n, "hit", "hits"), o.Data.Query,
		types, plural(types, "entity type", "entity types"))
	if !holdsEveryWord(o.Data.Hits) {
		// D-435: no live hit holds every word
		b.WriteString("No row holds every word; these hold some of them, most first.\n\n")
	}
	typeW, idW := 0, 0
	for _, h := range o.Data.Hits {
		typeW = max(typeW, textwidth.Width(h.Entity))
		idW = max(idW, textwidth.Width(hitID(h)))
	}
	for _, h := range o.Data.Hits {
		pretty := hitID(h)
		body := hitText(h)
		if h.Text != "" {
			// D-800: the whole text follows, so the line holds the title
			// alone: the snippet repeated a cut of the same words
			body = fullHitTitle(h)
		}
		if h.Source.Store != "" {
			body += "  " + StoreLabel(h.Source)
		}
		if h.InTrash {
			body += "  (in the trash)"
		}
		if h.History != "" {
			body += "  [" + h.History + "]"
		}
		if h.Stale != "" {
			body += "  [" + h.Stale + "]"
		}
		if h.CommentOn.ID != "" {
			// D-942: a comment says which row it is on and that it is history
			body = commentLabel(h.CommentOn) + titleSep + strings.TrimPrefix(body, "  ") + "  " + commentNote
		}
		if h.ViaComment != "" {
			body += "  (listed for its comment " + h.ViaComment + ")"
		}
		if body = strings.TrimPrefix(body, "  "); body != "" {
			body += "  "
		}
		fmt.Fprintf(&b, "%s  %s  %s(score=%.3f)\n", padRight(h.Entity, typeW), padRight(pretty, idW), body, h.Score)
		if h.Text != "" {
			b.WriteString(fullText(h.Text))
		}
	}
	if o.fullCapped > 0 {
		b.WriteString(fullCapNote(o.fullTop, o.fullCapped))
	}
	if o.Data.More {
		b.WriteString(morehits.Note(n, o.limit))
	}
	return b.String()
}

// holdsEveryWord reports whether a hit holds every word of the query.
// The first hit decided it before D-942; a row listed above its comment
// may hold fewer words than the comment under it.
// Only live hits count: the trash tail is matched by every word, never
// partial, and a list of trashed rows alone printed no such line.
func holdsEveryWord(hits []Hit) bool {
	live := false
	for _, h := range hits {
		if h.InTrash {
			continue
		}
		if live = true; !h.Partial {
			return true
		}
	}
	return !live
}

// fullCapNote is the line a --full search ends with when hits past the
// top printed only their snippet (devco D-800): how many, and the flag
// that prints them whole.
func fullCapNote(top, capped int) string {
	return fmt.Sprintf("note: the whole text is printed for the best %d %s; the other %d show their snippet; add --full-top %d to print them whole\n",
		top, plural(top, "hit", "hits"), capped, top+capped)
}

// fullHitTitle is the line text of a hit printed whole: its title (a
// resource's name and kind) without the snippet, else nothing, since the
// whole text follows under the line.
func fullHitTitle(h Hit) string {
	title := textcut.Cut(h.Title, titleMax, titleMax-len(textcut.Ellipsis))
	if title != "" && h.ResourceKind != "" {
		title += " (" + h.ResourceKind + ")"
	}
	return title
}

// fullIndent indents a --full hit's text under its line.
const fullIndent = "    "

// fullText is a --full hit's whole text, each line indented, then a blank
// line so the next hit reads apart.
func fullText(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString(strings.TrimRight(fullIndent+line, " ") + "\n")
	}
	return b.String() + "\n"
}

// FullText is the whole text a --full hit carries for row fields: its
// body, else the longest of the searched fields that is not the title.
func FullText(fields func(name string) string, searched []string, title string) string {
	if body := fields("body"); body != "" {
		return body
	}
	best := ""
	for _, f := range searched {
		if v := fields(f); f != title && len(v) > len(best) {
			best = v
		}
	}
	return best
}

// hitText is a hit's text on its line: the title (a resource's name and
// kind), then the snippet cut so its first match shows (D-436); without a
// title the snippet alone; without a snippet the label, else the start of
// the body.
func hitText(h Hit) string {
	title := textcut.Cut(h.Title, titleMax, titleMax-len(textcut.Ellipsis))
	if title != "" && h.ResourceKind != "" {
		title += " (" + h.ResourceKind + ")"
	}
	snip := ""
	if h.Snippet != "" && PlainSnippet(h.Snippet) != h.Title {
		snip = SnippetAround(h.Snippet, lineMax-len(title))
	}
	switch {
	case title != "" && snip != "":
		return title + titleSep + snip
	case title != "":
		return title
	case snip != "":
		return snip
	case h.Body != "":
		return textcut.Cut(h.Body, lineMax, lineMax-len(textcut.Ellipsis))
	}
	return textcut.Cut(h.Fallback, lineMax, lineMax-len(textcut.Ellipsis))
}

// Title and snippet layout of a hit line.
const (
	titleMax = 60
	titleSep = " — "
	// snippetMin is the least room a snippet gets after a long title.
	snippetMin = 40
	// matchLead is how much text before the first match a cut snippet keeps.
	matchLead = 24
)

// SnippetAround is snippet s (matches in <b>…</b>) as plain text of at
// most maxLen bytes (never under snippetMin) that shows its first match:
// when the match sits past the cut, the text before it is dropped to
// matchLead bytes, from a word start, behind "...". Before D-436 the line
// was cut from the start, so a match late in the snippet was cut away.
func SnippetAround(s string, maxLen int) string {
	maxLen = max(maxLen, snippetMin)
	at := strings.Index(s, "<b>") // no mark precedes the first: its offset in the plain text too
	plain := PlainSnippet(s)
	if closeAt := strings.Index(s, "</b>"); at > 0 && closeAt > at {
		end := closeAt - len("<b>") // the match's end in the plain text
		if end > maxLen-len(textcut.Ellipsis) {
			from := max(0, at-matchLead)
			for from < at && !utf8.RuneStart(plain[from]) {
				from++
			}
			if sp := strings.IndexByte(plain[from:at], ' '); sp >= 0 {
				from += sp + 1
			}
			plain = textcut.Ellipsis + strings.TrimPrefix(plain[from:], textcut.Ellipsis)
		}
	}
	return textcut.Cut(plain, maxLen, maxLen-len(textcut.Ellipsis))
}

// hitID is the id a hit shows: its short form, else the full id.
func hitID(h Hit) string {
	if h.Pretty != "" {
		return h.Pretty
	}
	return h.ID
}

// padRight pads s with spaces to w terminal cells (the short ids hold "…";
// devco:N-2: cells, not bytes or runes).
func padRight(s string, w int) string { return textwidth.PadRight(s, w) }

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
	indexed := make([]Target, 0, len(targets))
	for _, t := range targets {
		if !t.Roster { // the roster has no full-text index (D-489)
			indexed = append(indexed, t)
		}
	}
	rows, err := env.SearchStatus(ctx, indexed)
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

// rebuild validates --kind and asks env to rebuild every store's cache,
// or only --store's.
func rebuild(ctx context.Context, env Env, in RebuildIn) (RebuildOut, error) {
	if in.Kind != "" {
		if _, err := TargetsFor([]string{in.Kind}); err != nil {
			return RebuildOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("unknown entity %q", in.Kind)).
				WithHint("run `lore search status` to see the list")
		}
	}
	stores, err := env.RebuildSearch(ctx, strings.TrimSpace(in.Store))
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

// MarkContradicted marks each hit whose planned / not-yet claim a newer
// hit on the same subject states as done (devco:D-1027, bug hunt H-289):
// ContradictedBy names the newer hit and Stale says so, so the hit ranks
// below a fresh one with the same match (RankScore) and is never dropped.
// Only the hits are compared: a newer row on the subject holds its words,
// so it is a hit of the same query. Hits without a Claim (cards,
// comments, rows in the trash, history) neither are marked nor mark.
func MarkContradicted(hits []Hit) {
	docs := make([]staleclaim.Doc, 0, len(hits))
	for _, h := range hits {
		if h.Claim != nil && !h.InTrash {
			docs = append(docs, *h.Claim)
		}
	}
	if len(docs) < 2 {
		return
	}
	found := staleclaim.NewIndex(docs, nil).FindAll()
	if len(found) == 0 {
		return
	}
	by := make(map[string]staleclaim.Finding, len(found))
	for _, f := range found {
		by[f.ID] = f
	}
	for i := range hits {
		h := &hits[i]
		f, ok := by[h.ID]
		if !ok || h.Claim == nil || h.InTrash {
			continue
		}
		h.ContradictedBy = f.By
		if h.Stale == "" {
			h.Stale = f.Note()
		} else {
			h.Stale += "; " + f.Note()
		}
	}
}
