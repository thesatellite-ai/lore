// Package ask defines `lore ask` (layer L5, devco D-807, gate G-3): one
// call that answers a question from every store the caller reads, so an
// agent recalls with one read instead of a search, a show per hit and a
// task list.
//
//	ask.run --query "<question>"
//	  ─▶ read the question: content words, topics (decision, status,
//	     budget …), a debt direction (what do I owe X / what does X owe
//	     me), a date window (2026-11-02, the week of …, yesterday, last
//	     week, two Fridays ago, last weekend, in February, a holiday;
//	     against the clock; prior.go)
//	  ─▶ one lens (Env.AskLens): resources with fields and history, open
//	     tasks with owed_by / owed_to and --ref pointers
//	  ─▶ the resources it names: by name, a typo of a name, a field value
//	     (a nickname, a role)
//	  ─▶ the rows: for a question that is only a date the window's rows
//	     (journal days first); else the best rows about those resources
//	     and topics, a row dated near the window ranked higher, the row
//	     the first one replaced second (updates.go), newest first among
//	     equals; a name no row holds starts the text with a "not in
//	     memory" hint (abstain.go)
//	  ─▶ a question naming a file: what lore context --for-file picks for
//	     it comes first (file.go, devco D-815)
//	  ─▶ capped text (--max-chars): resources with changed fields, open
//	     tasks, the top rows in full, the ids it cites
//
// It only reads: the lens is read-only and sensitive values are masked.
package ask

import (
	"context"
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/logic/resquar"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (`lore ask`, a bare-noun shortcut of run).
const (
	Noun  = "ask"
	OpRun = "ask.run"
	// RunVerb is the verb the bare `lore ask` runs.
	RunVerb = "run"
)

// restNoun keeps the REST collection "ask" (the default plural would be
// "asks").
const restNoun = "ask"

// Query runs one read-only statement on the lens Env.AskLens opened.
type Query func(ctx context.Context, sql string) (opssql.Result, error)

// Env is what ask.run needs; *wire.App implements it.
//
// Invariant: AskLens opens one read-only lens over every store the call
// reads (the sql lens, sensitive values masked; repoOnly: the repo's own
// rows alone, `--store repo`), runs fn with it and closes it; fn may run
// several statements, all on the same lens. keeps says the lens keeps the
// answer of a FileOnly statement across calls (the read server's pooled
// lens, devco:D-842): ask then reads the statements that name nothing of
// the question and picks rows in Go; without it a statement that picks
// them in SQL is the cheaper single read (devco:D-846).
type Env interface {
	AskLens(ctx context.Context, repoOnly bool, fn func(q Query, keeps bool) error) error
}

// HomeEnv is an Env that names the store folder the repo is linked to
// (its org link, `lore whoami` reads): ask ranks that folder's rows above
// another folder's with the same match (devco D-963, Sync benchmark set
// 4: an own repo reads the whole company store, and another client's
// rows sharing a few words came before the repo's own notes). "" when
// the repo is linked to no folder; *wire.App implements it.
type HomeEnv interface {
	AskHome(ctx context.Context) string
}

// RunIn is the input of ask.run.
type RunIn struct {
	Query string `json:"query" arg:"0" rest:"true" desc:"the question, in your own words: who is alice-ng, what is the status of orbit, what did we decide for orbit and why, what do I owe alice-ng, what does alice-ng owe me, what happened yesterday / last week / on 2026-11-02, everything about alice-ng (a typo, a nickname or a role finds the resource too); name the file you are changing (add partial refunds in billing/refund.go) for the rules and hotfixes that apply to it"`
	// Store names the stores to read, as every other read's --store.
	Store    string `json:"store,omitempty" flag:"store" alias:"to" desc:"read this store alone instead of every attached one: <org> (every folder), <store>/<folder>, a store's name, me (every folder of your personal store), or repo (the repo's own rows); works from any folder; with none outside a code repo, where no setup covers the folder, it reads your personal store"`
	MaxChars int    `json:"max_chars,omitempty" flag:"max-chars" min:"200" desc:"cap the answer at this many characters (default 4000); what is left out is counted at the end"`
	Top      int    `json:"top,omitempty" flag:"top" min:"1" desc:"how many best rows to print in full (default 10, within --max-chars); a date question with no other words (what happened yesterday) prints every row of its dates"`
}

// Out is the output of ask.run: what answers the question, each item with
// its id and place, and the ids it cites.
type Out struct {
	Query string `json:"query" desc:"the question as asked"`
	When  string `json:"when,omitempty" desc:"the dates the question names, as read (2026-11-19 (yesterday))"`
	// Resources are the resources the question names, with their fields.
	Resources []Resource `json:"resources" desc:"the resources the question names (person, project, company …), each with its fields and the resources that link to it"`
	Tasks     []Task     `json:"tasks" desc:"the open tasks that answer it: for what do I owe X / what does X owe me every open one owed that way, else the open tasks of the resources it names"`
	Rows      []Row      `json:"rows" desc:"the rows that answer it, in full: the best ones newest first (a row dated near the dates the question names ranked higher), or, for a question that is only dates (what happened yesterday), every row of those dates, oldest first"`
	// Ranked is the answer's reading order (devco D-818): the item that
	// answers the question type first, the named resource after it as
	// context. Why a separate list: the three typed lists keep their JSON
	// shape, while the order an agent reads (and the text prints) changes
	// with the question: a status or decision row before its project, the
	// owed tasks before the person, the newest rows of each folder right
	// after the person for everything about X.
	Ranked []Ranked `json:"ranked" desc:"every item of the answer in reading order, the one that answers the question first: a status or decision row before its project, the owed tasks before the person, the person then the newest row of each folder for everything about X"`
	Cited  []string `json:"cited" desc:"the id of every item in the answer, in reading order"`
	Left   int      `json:"left,omitempty" desc:"how many items were left out to stay under --max-chars"`
	// File, Applies and Context answer a question that names a file
	// (file.go, devco D-815): what lore context --for-file --for-prompt
	// picks for it, printed before the ranked items.
	File    string    `json:"file,omitempty" desc:"the file the question names"`
	Applies []Applied `json:"applies,omitempty" desc:"the rules, hotfixes and notes that apply to that file and the question's words, as lore context --for-file --for-prompt picks them"`
	Context string    `json:"context,omitempty" desc:"lore context's text for that file (rules and hotfixes in full while they fit)"`
	// Abstain says the memory seems not to hold what the question asks
	// (abstain.go, devco D-897); the zero value, left out of the JSON,
	// when it does. A value, not a pointer: op.Define describes outputs
	// by reflection and has no pointer type.
	Abstain Abstain `json:"abstain,omitzero" desc:"set when the question names something (a word written with a capital, such as a place or a pet's name) that no row holds: the rows are then the nearest the memory holds, not the answer"`
	// Conflicts are printed rows stating different values for the same
	// named field (conflicts.go, bug hunt H-239): the text answer prints
	// one line each so an agent checks which is current instead of
	// taking the first.
	Conflicts []Conflict `json:"conflicts,omitempty" desc:"set when two rows the answer prints state different values for the same named field (a snake, kebab or dotted key, a camelCase name, a --flag or a word in backticks: X = V, X: V, X defaults to V, no default for X, X is V ms; units compared after normalising, 5 s = 5000 ms), at most 3; check which is current with lore show --id <id> and fix or supersede the other"`
	maxChars  int
	// leftBrief is how many of Left are resources the question only
	// touches, left out by their share of the cap (devco D-963).
	leftBrief int
	note      string
	// lead is how many of Rows (the first ones) are an everything-about
	// answer's newest row of each folder, printed one line each (devco
	// D-822); 0 for any other question.
	lead int
	// identity is a who-is question with a resource it names, answer
	// the list that answers first (leadOf) and asked the question's words:
	// the text prints a who-is answer as one-line cards and a debt answer
	// as bare task lines (devco D-827); --json keeps every field.
	identity bool
	answer   answerFirst
	asked    []string
	// q is the question asked: the cut note names the words of it a cut
	// row left out (leftout.go).
	q question
	// prior is a date question with words besides the date: its rows are
	// the best rows, those dated near the window ranked up (prior.go),
	// not the window's rows oldest first.
	prior bool
}

// Ranked names one item of an answer: which list holds it and its id.
type Ranked struct {
	Kind string `json:"kind" desc:"resource, task or row: the list (resources, tasks, rows) that holds the item"`
	ID   string `json:"id"`
}

// The kinds of Ranked.
const (
	KindResource = "resource"
	KindTask     = "task"
	KindRow      = "row"
)

// Resource is one resource in an answer.
type Resource struct {
	ID    string `json:"id"`
	Kind  string `json:"kind" desc:"its kind (person, project, company …)"`
	Name  string `json:"name"`
	Place string `json:"place" desc:"where it is stored (<store>/<folder>)"`
	Match string `json:"match" desc:"how the question named it: name, alias (one of its aliases), typo (a name or alias a letter or two off) or field (a nickname, a role kept as a field)"`
	// Aliases are its other names (lore resource alias add, devco D-817).
	Aliases []string `json:"aliases,omitempty" desc:"its other names: nicknames, full names, roles"`
	Fields  []Field  `json:"fields" desc:"its field values (sensitive ones masked)"`
	Linked  []Linked `json:"linked" desc:"the resources whose fields name it (the people of a company, the projects a person owns)"`
	// touched says the question does not name it whole (a part of its
	// name or a field value found it, match.whole): under rows it prints
	// as one short line and such lines share a small part of the cap
	// (devco D-963). --json keeps every field.
	touched bool
	// parts is how many of the question's words found it (match.said):
	// of the resources a question only touches, the one it touches most
	// takes the answer's one resource slot (devco D-968).
	parts int
}

// Field is one field value of a resource; Was and ChangedAt say a value
// that changed.
type Field struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Was       string `json:"was,omitempty" desc:"the value before the newest change"`
	ChangedAt string `json:"changed_at,omitempty" desc:"the day the value changed (YYYY-MM-DD)"`
}

// Linked is a resource whose field names another.
type Linked struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	Via    string   `json:"via" desc:"the field that names it"`
	Fields []string `json:"fields,omitempty" desc:"its other field values, as name value"`
}

// Task is one open task in an answer.
type Task struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Place   string `json:"place"`
	OwedBy  string `json:"owed_by,omitempty"`
	OwedTo  string `json:"owed_to,omitempty"`
	Created string `json:"created_at,omitempty"`
}

// Row is one row in an answer, in full.
type Row struct {
	ID      string   `json:"id"`
	Table   string   `json:"table"`
	Place   string   `json:"place"`
	At      string   `json:"at,omitempty" desc:"the day it is about (its day metadata, valid_at, else when written)"`
	Title   string   `json:"title,omitempty"`
	Body    string   `json:"body"`
	Matched []string `json:"matched" desc:"the topics and words of the question it holds"`
	Note    string   `json:"note,omitempty" desc:"newest: it replaces <id> from <day>, when an older row says the same thing"`
	// History is set on a row that is no longer the current answer
	// (devco D-820): superseded by another row, past its valid_until, or
	// replaced by a newer row of the same folder on the same thing. Such
	// a row ranks after every current row and an agent reads it as what
	// used to be true.
	// FullChars is set on a row cut to the part of it that answers the
	// question: a row longer than the answer's --max-chars (devco D-859),
	// or a markdown row longer than 1,500 characters (devco D-892, even
	// under the cap). Body is then only that part (a markdown row: its
	// best sections, each after a "§ <heading path>" line, devco D-888;
	// else its best paragraph, with … where text was left out), and
	// FullChars is the whole body's length (lore show --id prints it).
	FullChars int    `json:"full_chars,omitempty" desc:"set when the row was cut to the part that answers the question: a row longer than 1,500 characters prints its best sections by heading (at most 1,000 characters, best first, each after a line § <heading path>, the first marked (matching section)) or else its best paragraph (… marking text left out), and a row longer than --max-chars the same within the room left; this is the whole body's length; lore show --id <id> prints it whole"`
	History   string `json:"history,omitempty" desc:"set when the row is no longer the current answer, read it as history: superseded by <id>, valid until <day>, or replaced by <id> from <day> (a newer row of the same folder on the same thing)"`
	// LeftOut is set with FullChars when the part of the row left out
	// holds question words the printed part does not (leftout.go, devco
	// D-974): the answer may be there, lore show --id prints it.
	LeftOut []string `json:"left_out,omitempty" desc:"set with full_chars when the part of the row left out holds words of the question the printed part does not (at most 6, in the question's order): the answer may be in the part not printed; lore show --id <id> prints it whole"`
	// Comments are the comments on the row that hold a word of the
	// question (devco D-942): history, printed under the row, never an
	// answer of their own.
	Comments []Comment `json:"comments,omitempty" desc:"comments on this row holding a word of the question, oldest first; read them as history (what someone said about the row then), the row is the answer"`
	// Stale is set on a row stating a fact about code whose evidence
	// changed in git after it was verified (devco:D-941): it ranks below a
	// fresh row with the same match.
	Stale string `json:"stale,omitempty" desc:"set when the row states a fact whose evidence files (fact_evidence) changed in git after it was verified (fact_verified_at): stale: evidence changed since <day> (<files>); check it before relying on it, it ranks below a fresh row with the same match"`
	// Reverify is what clears Stale after reading the row and the code
	// again (devco:D-953), the words lore lint and lore show print.
	Reverify string `json:"reverify,omitempty" desc:"set with stale: when it still holds: lore meta set --id <id> [--store <store>] --pair fact_verified_at=<day>; when it does not: edit the row and its fact_evidence"`
	// readTitle and readBody are Title and Body as the text answer prints
	// them: each ${res:…} naming a resource the ask read replaced by the
	// resource's name (refs.Readable, devco D-822). --json keeps the
	// stored text.
	readTitle, readBody string
	// subject is the question's subject terms across the store
	// (subject.go, devco D-973): the words a cut of this row's text keeps
	// first. Nil when the row was not ranked among scored rows.
	subject *subjectTerms
	// replaces is the id of the older row on the same thing this newest
	// row replaces (the id Note names), "" when none: the text answer
	// leads with a "now:" line for it (devco D-836).
	replaces string
	// factState and verified are the row's fact_state and
	// fact_verified_at day (markFacts): a conflict line names the live,
	// newer verified row first (conflicts.go).
	factState, verified string
}

// RoutedStore implements the op package's StoreRouter, as sql.run does
// (devco D-799): the question is asked of the store --store names alone.
func (in RunIn) RoutedStore() string { return in.Store }

// Ops returns the ask operation bound to env.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Answer a question in one call: the resources it names, their open tasks and the best rows in full, capped",
			Permission: op.PermRead, Access: op.AccessRead, RESTNoun: restNoun,
			Errors: []errs.Code{
				errs.InvalidInput, errs.NotFound, errs.DBNotFound, errs.NotAStore, errs.StoreUnreachable,
				errs.SQLTimeout, errs.Cancelled, errs.OldLore, errs.Internal,
			},
			Examples: []op.Example{
				{Title: "who someone is", CLI: `lore ask --query "who is alice-ng"`, Want: "ask:"},
				{Title: "what you owe, from any folder", CLI: `lore ask --store me --query "what do I owe alice-ng"`, Want: "ask:"},
				{Title: "a day of your journal", CLI: `lore ask --store me --query "what happened yesterday" --json`, Want: "rows"},
			},
		}, func(ctx context.Context, in RunIn) (Out, error) { return run(ctx, env, in) }),
	}
}

// run reads the question, loads what can answer it and builds the answer.
func run(ctx context.Context, env Env, in RunIn) (Out, error) {
	if strings.TrimSpace(in.Query) == "" {
		return Out{}, errs.New(errs.InvalidInput, "ask needs a question").
			WithHint(`lore ask --query "what is the status of orbit"`)
	}
	q := parseQuestion(in.Query, opclock.Now(env).UTC())
	if h, ok := env.(HomeEnv); ok {
		// also with --store <org>, which reads every folder: the repo's
		// own folder still comes first (a store without it has no row
		// there, so nothing changes)
		q.home = h.AskHome(ctx)
	}
	var d data
	err := env.AskLens(ctx, in.Store == string(store.SourceRepo), func(run Query, keeps bool) error {
		var lerr error
		if d.resources, d.tasks, lerr = loadTyped(ctx, run); lerr != nil {
			return lerr
		}
		ms := matchResources(q, d.resources)
		if q.owe != oweNone || (isIdentity(q.text) && len(ms) > 0) {
			return nil // answered by resources and tasks
		}
		var c *corpus
		var fill func(*textRow)
		d.rows, c, fill, lerr = loadRows(ctx, run, q, ms, keeps)
		if lerr == nil && c != nil {
			vf, _ := env.(VectorFile)
			d.view = viewOf(q, c, vf)
			d.rows = withMeaning(q, ms, d.rows, c, d.view, fill)
		}
		if _, checks := env.(FactEnv); lerr == nil && checks && len(d.rows) > 0 {
			// devco:D-941 stale evidence and devco D-963 fact_state /
			// fact_verified_at ranking: only an env that can check them
			d.facts, lerr = loadFacts(ctx, run)
		}
		return lerr
	})
	if err != nil {
		return Out{}, err
	}
	markStale(ctx, env, d.rows, d.facts)
	markFacts(d.rows, d.facts)
	maxChars := in.MaxChars
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	if q.file == "" {
		return build(q, d, in.Top, maxChars), nil
	}
	text, applied, note := fileContext(ctx, env, q.file, q.rest, fileBudget(maxChars))
	picked := map[string]bool{}
	for _, a := range applied {
		picked[a.ID] = true
	}
	// a row the file's context already printed is not printed twice
	d.rows = slices.DeleteFunc(d.rows, func(r textRow) bool { return picked[r.ID] })
	// the rows get what the file's context left, at least 1: build reads
	// 0 or less as "no --max-chars" (4000), which let a context that
	// filled a small cap be followed by 4000 characters of rows; the best
	// row is still printed, cut (devco D-859)
	out := build(q, d, in.Top, max(maxChars-len(text), 1))
	out.File, out.Context, out.note, out.maxChars = q.file, text, note, maxChars
	out.Applies = applied
	if out.Applies == nil {
		out.Applies = []Applied{}
	}
	out.Cited = append(printedIDs(text, applied), out.Cited...)
	return out, nil
}

// cell is a lens cell as text ("" for NULL).
func cell(v value.Value) string {
	if v == nil {
		return ""
	}
	return v.String()
}

// LensTables are the tables the statements of ask read (each through its
// "<table>_all" view): the lens is built with these alone (devco D-813),
// so a call does not read and view the other tables of every cache.
// TestLensTablesCoverTheStatements fails when a statement reads another.
var LensTables = []string{"resources", "kinds", "kind_fields", "tasks", "row_meta", "knowledge_refs", "row_refs", "memories", "decisions", "rules", "patterns", "incidents", "hotfixes", "architecture_notes", "cookbook_recipes", "workflows", "comments"}

// typedSQL reads the resources (with kind name, fields and history), the
// allowed value ids of each kind field, the open tasks, their owed_by /
// owed_to metadata and their --ref pointers, in one statement: a first
// column says which a line is.
const typedSQL = `SELECT 'res', r.id, r.place, COALESCE(k.name, r.kind), r.name, r.fields, r.history, r.updated_at, r.aliases
FROM resources_all r LEFT JOIN kinds_all k ON k.id = r.kind WHERE r.archived_at IS NULL
UNION ALL SELECT 'fld', f.id, '', '', '', f.value_ids, '', '', ''
FROM kind_fields_all f WHERE COALESCE(f.value_ids, '') <> ''
UNION ALL SELECT 'tsk', t.id, t.place, t.status, t.title, '', '', t.created_at, ''
FROM tasks_all t WHERE t.status NOT IN ('done', 'cancelled')
UNION ALL SELECT 'met', m.entity_id, '', m.key_name, m.value_text, '', '', '', ''
FROM row_meta_all m WHERE m.entity_table = 'tasks' AND m.item_idx >= 0 AND m.key_name IN ('owed_by', 'owed_to')
UNION ALL SELECT 'ref', kr.src_id, '', kr.relation, kr.dst_id, '', '', '', ''
FROM knowledge_refs_all kr WHERE kr.src_table = 'tasks' AND kr.dst_table = 'resources'`

// FileOnly reports whether statement's answer depends only on the cache
// files the lens reads (no clock, no per-call input), so a long-lived
// process may keep it until a file changes (devco:D-842: the read server
// keeps typedSQL's answer, read by every question, across calls; devco:D-846
// adds textSQL and daysSQL, which name no word or day of the question).
func FileOnly(statement string) bool {
	return statement == typedSQL || statement == textSQL || statement == daysSQL || statement == commentsSQL || statement == factSQL
}

// loadTyped reads the resources and the open tasks.
func loadTyped(ctx context.Context, run Query) ([]resourceRow, []taskRow, error) {
	res, err := run(ctx, typedSQL)
	if err != nil {
		return nil, nil, err
	}
	res.Rows = canonicalLines(res.Rows, typedLineKey) // devco:D-979: not the cache's insert order
	var rs []resourceRow
	var ts []taskRow
	at := map[string]int{}
	seenRes := map[string]bool{}
	type pending struct{ kind, id, a, b string }
	var later []pending
	// a closed value is stored as its val_ id: the fields are read once
	// every field's value ids are (the lines come in any order)
	rawFields := map[string]string{}
	valueIDs := map[string]map[string]string{}
	for _, row := range res.Rows {
		if len(row) < 8 {
			continue
		}
		c := make([]string, len(row))
		for i, v := range row {
			c[i] = cell(v)
		}
		switch c[0] {
		case "res":
			if seenRes[c[1]] { // nearest store first
				continue
			}
			seenRes[c[1]] = true
			r := resourceRow{ID: c[1], Place: c[2], Kind: c[3], Name: c[4], History: historyOf(c[6]), Updated: c[7]}
			if len(c) > 8 {
				r.Aliases = aliasesOf(c[8])
			}
			rawFields[r.ID] = c[5]
			rs = append(rs, r)
		case "fld":
			// a field's copies (store, extension) add their ids together
			if valueIDs[c[1]] == nil {
				valueIDs[c[1]] = map[string]string{}
			}
			maps.Copy(valueIDs[c[1]], resquar.ValueIDsOf([]byte(c[5])))
		case "tsk":
			if _, dup := at[c[1]]; dup {
				continue
			}
			at[c[1]] = len(ts)
			ts = append(ts, taskRow{ID: c[1], Place: c[2], Status: c[3], Title: c[4], Created: c[7]})
		default:
			later = append(later, pending{c[0], c[1], c[3], c[4]})
		}
	}
	for i := range rs {
		rs[i].Fields = fieldsOf(rawFields[rs[i].ID], valueIDs)
	}
	for _, p := range later {
		i, ok := at[p.id]
		if !ok {
			continue // a done task's metadata
		}
		switch {
		case p.kind == "met" && p.a == "owed_by":
			ts[i].OwedBy = p.b
		case p.kind == "met" && p.a == "owed_to":
			ts[i].OwedTo = p.b
		case p.kind == "ref":
			ts[i].Refs = append(ts[i].Refs, taskRef{Relation: p.a, ResID: p.b})
		}
	}
	return rs, ts, nil
}

// aliasesOf reads resources.aliases (a JSON array of text); a bad value
// reads as none.
func aliasesOf(s string) []string {
	var out []string
	if s == "" || json.Unmarshal([]byte(s), &out) != nil {
		return nil
	}
	return out
}

// fieldsOf reads resources.fields: {fld_id: {name, v}} → name/value pairs
// in name order. A closed value stored as its val_ id (with its name in
// v_name, OQ-AV) reads as its current name by valueIDs (fld_ id → val_ id
// → name), else as v_name, as resource list and search print it
// (resquar.Shown). Why (devco D-949, public issue #157): the cards of an
// answer printed "status: val_…" where resource list prints "active".
func fieldsOf(s string, valueIDs map[string]map[string]string) []fieldValue {
	if s == "" {
		return nil
	}
	var m map[string]struct {
		Name  string          `json:"name"`
		V     json.RawMessage `json:"v"`
		VName json.RawMessage `json:"v_name"`
	}
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	out := make([]fieldValue, 0, len(m))
	for fid, f := range m {
		var vName []byte
		if len(f.VName) > 0 && string(f.VName) != "null" {
			vName = f.VName
		}
		v := resquar.Shown(resquar.Field{ValueIDs: valueIDs[fid]}, f.V, vName)
		out = append(out, fieldValue{Name: f.Name, Value: rawText(v)})
	}
	sortFields(out)
	return out
}

// rawText is a JSON value as text: a string unquoted, anything else as
// written.
func rawText(r json.RawMessage) string {
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(r))
}

// sortFields orders fields by name, so an answer prints the same each
// time.
func sortFields(fs []fieldValue) {
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && (fs[j].Name < fs[j-1].Name || fs[j].Name == fs[j-1].Name && fs[j].Value < fs[j-1].Value); j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}

// historyOf reads resources.history (oldest first).
func historyOf(s string) []historyEntry {
	if s == "" {
		return nil
	}
	var hs []struct {
		Field string          `json:"field"`
		From  json.RawMessage `json:"from"`
		To    json.RawMessage `json:"to"`
		At    string          `json:"at"`
	}
	if json.Unmarshal([]byte(s), &hs) != nil {
		return nil
	}
	out := make([]historyEntry, 0, len(hs))
	for _, h := range hs {
		e := historyEntry{Field: h.Field, At: h.At}
		if len(h.From) > 0 {
			e.From = rawText(h.From)
		}
		if len(h.To) > 0 {
			e.To = rawText(h.To)
		}
		out = append(out, e)
	}
	return out
}

// daysSQL reads every row's day metadata. It names no day, so it is
// clock-free and input-free and the read server keeps its answer until a
// cache file changes (FileOnly, devco:D-846). Read once on its own:
// row_meta_all reads every row's metadata JSON, and as a subquery of each
// text table's arm it ran once per table (devco D-813).
const daysSQL = "SELECT entity_id, value_text FROM row_meta_all WHERE key_name = 'day' AND item_idx >= 0"

// loadDays reads the day metadata a date question needs (row id → its
// days, as YYYY-MM-DD): every item inside near (the window widened by the
// question's fuzz) and every item of a row written inside near. A lens
// that keeps answers (keeps) reads daysSQL and metaDayOf applies the same
// cut in Go; any other reads only those items (one read: filtering in SQL
// is cheaper than handing every item over). Why (devco D-903, bug hunt
// H-158): reading only the window's items, a row about a day just outside
// the window was ranked by the day it was written, and a row written in
// the window about a day far from it was listed as one of its rows.
func loadDays(ctx context.Context, run Query, near dayRange, keeps bool) (map[string][]string, error) {
	stmt := daysSQL
	if !keeps {
		var written []string
		for _, t := range textTables {
			at := "COALESCE(" + t.extra + ", m.created_at)"
			written = append(written, "SELECT m.id FROM "+t.table+"_all m WHERE "+at+" >= "+sqlText(near.since)+" AND "+at+" < "+sqlText(near.end))
		}
		stmt += " AND (value_text >= " + sqlText(near.since) + " AND value_text < " + sqlText(near.end) + " OR entity_id IN (" + strings.Join(written, " UNION ALL ") + "))"
	}
	res, err := run(ctx, stmt)
	if err != nil {
		return nil, err
	}
	days := map[string][]string{}
	for _, row := range res.Rows {
		if len(row) < 2 || row[1] == nil {
			continue
		}
		id := cell(row[0])
		days[id] = append(days[id], row[1].String())
	}
	return days, nil
}

// dayRange is a range of days [since, end) as YYYY-MM-DD, compared as
// text: a stored date (2026-11-02T00:00:00.000000000Z) sorts after its
// day and before the next one.
type dayRange struct{ since, end string }

// holds reports whether day (or a stored date) falls in r.
func (r dayRange) holds(day string) bool { return day >= r.since && day < r.end }

// metaDayOf is the day a row is about among its day metadata items: the one
// nearest q's window (the later of two as near), read from the items
// inside near, or from every item when the row was written (at, a stored
// date) inside near; "" when none counts. loadDays reads exactly the
// items this can pick, so a one-shot and a kept read agree.
func metaDayOf(q question, items []string, at string, near dayRange) string {
	all := near.holds(at)
	best, bestGap := "", math.Inf(1)
	for _, d := range items {
		if !all && !near.holds(d) {
			continue
		}
		t, err := time.Parse(dates.DayLayout, d)
		if err != nil {
			continue
		}
		if g := gapDays(q.window, spanW(t, t)); g < bestGap || g == bestGap && d > best {
			best, bestGap = d, g
		}
	}
	return best
}

// textTables are the tables ask reads rows from, with the SQL of their
// title, text, date and valid-until columns, and whether the table has a
// superseded_by_id column (the notes of how the system is built have
// none: a newer note edits the old one).
var textTables = []struct {
	table, title, body, extra, until string
	supersedes                       bool
}{
	{"memories", "''", "m.body", "m.valid_at", "m.valid_until", true},
	{"decisions", "m.title", "m.body", "NULL", "NULL", true},
	{"rules", "''", "m.body", "NULL", "NULL", true},
	{"patterns", "m.title", "m.body", "NULL", "NULL", true},
	{"incidents", "m.title", "m.body", "NULL", "NULL", false},
	// devco D-815: a hotfix is the trap a coding question must not miss
	{"hotfixes", "m.title", "m.body", "NULL", "NULL", true},
	// devco D-963 (Sync benchmark set 4): the notes that say how the
	// system is built, how a recurring task is done and which steps a
	// flow takes are answers as much as a memory or a decision is; ask
	// read none of them, so the as-built architecture note that held the
	// whole answer was never printed while lore search ranked it first.
	{"architecture_notes", "m.title", "m.body", "NULL", "NULL", false},
	{"cookbook_recipes", "m.title", "m.body", "NULL", "NULL", false},
	{"workflows", "m.name", "m.body", "NULL", "NULL", false},
}

// Columns of a text statement line: kind ('row' or 'ref'), then for a row
// its table, id, place, title, text, date (COALESCE(extra, created_at)),
// imported, superseded-by, valid-until, the lowered "title text" the
// words are found in (NULL when the title or the text is NULL, which no
// word matches, as LIKE on NULL does not), and its created_at and
// updated_at (which copy of an id two stores hold answers: pickLines);
// for a ref its entity table, entity id and the resource id it references.
const (
	colKind, colTable, colID, colPlace, colTitle, colBody, colAt, colImported, colSuperseded, colUntil, colLower = 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10
	colCreated, colUpdated                                                                                       = 11, 12
	colRefID                                                                                                     = 3
	// textCols is the fewest cells a line has; colCreated and colUpdated
	// follow when the statement reads them (a line without them keeps the
	// nearest store's copy, newerCopy)
	textCols = 11
)

// textStatement is the statement reading the text tables' rows that pass
// cond (SQL with %T, %B and %V for each table's title, text and date; ""
// for every live row) and the row_refs lines naming one of refIDs (quoted;
// nil for every line, empty for none). Every arm has the textCols
// columns.
func textStatement(cond string, refIDs []string) string {
	var parts []string
	for _, t := range textTables {
		var where []string
		if cond != "" {
			where = append(where, "("+strings.NewReplacer("%T", t.title, "%B", t.body, "%V", "COALESCE("+t.extra+", m.created_at)").Replace(cond)+")")
		}
		imported := "0"
		if t.table == "memories" {
			imported = "(m.source_kind = 'imported')"
		}
		superseded := "''"
		if t.supersedes {
			superseded = "COALESCE(m.superseded_by_id, '')"
		}
		// every text table has archived_at (devco D-924): an archived row
		// is not an answer, an incident's included
		where = append(where, "m.archived_at IS NULL")
		stmt := "SELECT 'row', '" + t.table + "', m.id, m.place, COALESCE(" + t.title + ", ''), COALESCE(" + t.body + ", ''), COALESCE(" + t.extra + ", m.created_at), " + imported + ", " + superseded + ", COALESCE(" + t.until + ", ''), lower(" + t.title + " || ' ' || " + t.body + "), COALESCE(m.created_at, ''), COALESCE(m.updated_at, '')" +
			" FROM " + t.table + "_all m"
		if len(where) > 0 {
			stmt += " WHERE " + strings.Join(where, " AND ")
		}
		parts = append(parts, stmt)
	}
	refs := "SELECT 'ref', entity_table, entity_id, ref_id, '', '', '', 0, '', '', NULL, '', '' FROM row_refs_all"
	switch {
	case refIDs == nil:
	case len(refIDs) == 0:
		refs += " WHERE 0"
	default:
		refs += " WHERE ref_id IN (" + strings.Join(refIDs, ", ") + ")"
	}
	return strings.Join(append(parts, refs), "\nUNION ALL ")
}

// textSQL reads every live row of the text tables and every row_refs
// line: no word, id or day of the question in it, so it is clock-free and
// input-free and the read server keeps its answer until a cache file
// changes (FileOnly). Why (devco:D-846): the per-question statement (a
// LIKE per word over every text table, compiled with the question's
// literals) cost ~13 ms of every warm served status, decision or date
// question.
var textSQL = textStatement("", nil)

// likeText is the text a row must contain to hold word w, as
// `lower(title || ' ' || text) LIKE '%<lower(w)>%'` decides it: SQLite's
// lower() folds ASCII only and LIKE folds ASCII case only, so the row's
// lowered text must contain strings.ToLower(w) byte for byte.
func likeText(w string) string { return strings.ToLower(w) }

// sqlText quotes s as an SQL string literal.
func sqlText(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// loadRows reads the rows that can answer q: rows holding a word of the
// question, a topic word or a matched resource's name, rows referencing a
// matched resource, and for a date question the rows of its window; and
// corpus, every live row (nearest store first), which corpus.go indexes.
// Both come from textSQL, which a lens that keeps answers (keeps: the read
// server) keeps across calls; rowFilter.answers picks the rows as the
// per-question LIKE statement did (TestTextRowsMatchTheLikeStatement
// compares the two). Why every row (devco D-902): a row nearest the
// question in meaning may hold none of its words, so one-shot and served
// calls read the same corpus and print the same answer.
func loadRows(ctx context.Context, run Query, q question, ms []match, keeps bool) (rows []textRow, c *corpus, fill func(*textRow), err error) {
	terms := rowTerms(q, ms)
	f := rowFilter{pats: make([]string, 0, len(terms)), ids: map[string]bool{}, win: !q.window.IsZero()}
	for _, t := range terms {
		f.pats = append(f.pats, likeText(t))
	}
	for _, m := range ms {
		f.ids[m.res.ID] = true
	}
	// with a window and a matched resource the window decides; the
	// resource filter runs in Go (answer.go)
	f.byText = !f.win || len(ms) == 0
	if f.win {
		f.since, f.end = q.window.Since.Format(time.DateOnly), q.window.End.Format(time.DateOnly)
		if q.window.End.IsZero() {
			f.end = openEnd // "after march 3rd": every day after it
		}
		f.near = nearRange(q, dayRange{f.since, f.end})
		if f.days, err = loadDays(ctx, run, f.near, keeps); err != nil {
			return nil, nil, nil, err
		}
	}
	if len(terms) == 0 && !f.win {
		return nil, nil, nil, nil
	}
	// every live row, kept or not: the rows nearest the question in
	// meaning are among the rows holding none of its words, and BM25
	// weighs words by the whole store (corpus.go, devco D-902)
	res, err := run(ctx, textSQL)
	if err != nil {
		return nil, nil, nil, err
	}
	res.Rows = canonicalLines(res.Rows, textLineKey) // devco:D-979: not the cache's insert order
	notes, err := loadComments(ctx, run)             // D-942: comments make their row answer
	if err != nil {
		return nil, nil, nil, err
	}
	refs := map[string][]string{}
	for _, row := range res.Rows {
		if len(row) < textCols || cell(row[colKind]) != "ref" || !f.ids[cell(row[colRefID])] {
			continue
		}
		ent := cell(row[colID])
		refs[ent] = append(refs[ent], cell(row[colRefID]))
	}
	f.refs = refs
	// fill sets a row's day metadata for the question (metaDayOf, devco
	// D-903): the rows picked here and the rows joining by meaning
	fill = func(r *textRow) {
		r.comments = notes[r.ID]
		if f.win {
			r.Day = metaDayOf(q, f.days[r.ID], r.stored, f.near)
		}
	}
	c = corpusOf(res.Rows)
	for i := range pickLines(res.Rows) {
		row := res.Rows[i]
		id := cell(row[colID])
		if f.answers(row, id) || f.byText && commentsHold(notes[id], f.pats) {
			r := c.rows[c.at[id]]
			r.Refs = refs[id]
			fill(&r)
			rows = append(rows, r)
		}
	}
	return rows, c, fill, nil
}

// rowTerms are the words a row must hold (one of) to be read for q: the
// matched resources' names, aliases and the words the question found
// them by, and always the question's words and topic words. Why the
// question's words with a matched resource too (devco D-949, public issue
// #157): when no row is about the resource, the rows holding the words
// still answer (build), so ask never answers with the resource alone
// while rows match.
func rowTerms(q question, ms []match) []string {
	var terms []string
	for _, m := range ms {
		terms = append(terms, m.res.Name)
		// a note that names it by an alias ("lunch with Bobby") is about it
		terms = append(terms, m.res.Aliases...)
		// and one that says the word the question found it by (the part
		// widget of widget_api)
		terms = append(terms, m.said...)
	}
	terms = append(terms, q.words...)
	for _, t := range q.topics {
		terms = append(terms, t.row...)
	}
	return terms
}

// rowOfLine is the text row of a text statement's row line (Refs and Day
// unset).
func rowOfLine(row []value.Value, id string) textRow {
	t, _ := time.Parse(time.RFC3339Nano, cell(row[colAt]))
	until, _ := time.Parse(time.RFC3339Nano, cell(row[colUntil]))
	return textRow{Table: cell(row[colTable]), ID: id, Place: cell(row[colPlace]), Title: cell(row[colTitle]), Body: cell(row[colBody]), At: t, Imported: cell(row[colImported]) == "1", SupersededBy: cell(row[colSuperseded]), ValidUntil: until, stored: cell(row[colAt])}
}

// withMeaning adds to rows the rows of c nearest q in meaning that rows
// does not hold yet (at most meaningRows), marked meaning. A row joins
// only when it is at least as near the question as the nearest row
// holding a word, and at least meaningFloor: a row the words found
// answers better than a row merely about the same things, so the meaning
// rows come in where the words miss, not as padding after every answer.
// Only for a question asked in words about no named resource, not an
// everything-about, debt or date-only one: a named resource's answer is
// the rows about it, and a date question's the rows of its dates. Why
// (devco D-902, hunt H-157): a question asked in other words than the
// note that answers it ("which physician do I see" for "My doctor is Dr
// Patel …") found nothing.
func withMeaning(q question, ms []match, rows []textRow, c *corpus, v *meaningView, fill func(*textRow)) []textRow {
	if v == nil || len(ms) > 0 || q.about || q.owe != oweNone || len(q.words) == 0 {
		return rows
	}
	floor := meaningFloor
	skip := make(map[int]bool, len(rows))
	for i := range rows {
		if p, ok := v.at[rows[i].ID]; ok {
			skip[p] = true
			floor = max(floor, v.cos[p])
		}
	}
	for _, p := range v.nearest(meaningRows, floor, skip) {
		r := c.rows[p]
		r.meaning = true
		if fill != nil {
			fill(&r)
		}
		rows = append(rows, r)
	}
	return rows
}

// openEnd is the end of a window open to the future, as a day: after
// every stored day.
const openEnd = "9999-12-31"

// rowFilter is what decides that a text row can answer a question: the
// lowered words (pats), the matched resources (ids) and the rows that
// reference one (refs), whether words decide (byText), and a date
// question's window [since, end) with its day metadata (days).
type rowFilter struct {
	pats       []string
	ids        map[string]bool
	refs       map[string][]string
	byText     bool
	win        bool
	since, end string
	// near is the window widened by the question's fuzz (nearRange);
	// days are the day metadata items loadDays read for it
	near dayRange
	days map[string][]string
}

// nearRange is window w widened by q's fuzz on each side: a row about a
// day in it still ranks as dated near the window (timeBonus), so its day
// metadata is read. An open side stays open.
func nearRange(q question, w dayRange) dayRange {
	n := int(math.Ceil(q.fuzz))
	if !q.window.Since.IsZero() {
		w.since = q.window.Since.AddDate(0, 0, -n).Format(time.DateOnly)
	}
	if !q.window.End.IsZero() {
		w.end = q.window.End.AddDate(0, 0, n).Format(time.DateOnly)
	}
	return w
}

// answers reports whether a text statement row can answer: by text
// (byText) its lowered text holds one of pats or it references a matched
// resource; for a date question its date falls in [since, end) or its day
// metadata does.
func (f *rowFilter) answers(row []value.Value, id string) bool {
	if f.byText {
		if low := row[colLower]; low != nil {
			text := low.String()
			for _, p := range f.pats {
				if strings.Contains(text, p) {
					return true
				}
			}
		}
		if _, ok := f.refs[id]; ok {
			return true
		}
	}
	if f.win {
		if at := cell(row[colAt]); at >= f.since && at < f.end {
			return true
		}
		if slices.ContainsFunc(f.days[id], dayRange{f.since, f.end}.holds) {
			return true
		}
	}
	return false
}
