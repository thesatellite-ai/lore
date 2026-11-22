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
	"math"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
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
	Abstain  Abstain `json:"abstain,omitzero" desc:"set when the question names something (a word written with a capital, such as a place or a pet's name) that no row holds: the rows are then the nearest the memory holds, not the answer"`
	maxChars int
	note     string
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
	FullChars int    `json:"full_chars,omitempty" desc:"set when the row was cut to the part that answers the question: a markdown row longer than 1,500 characters prints its best sections by heading (at most 1,000 characters, best first, each after a line § <heading path>), and a row longer than --max-chars its best sections or else its best paragraph (… marking text left out); this is the whole body's length; lore show --id <id> prints it whole"`
	History   string `json:"history,omitempty" desc:"set when the row is no longer the current answer, read it as history: superseded by <id>, valid until <day>, or replaced by <id> from <day> (a newer row of the same folder on the same thing)"`
	// readTitle and readBody are Title and Body as the text answer prints
	// them: each ${res:…} naming a resource the ask read replaced by the
	// resource's name (refs.Readable, devco D-822). --json keeps the
	// stored text.
	readTitle, readBody string
	// replaces is the id of the older row on the same thing this newest
	// row replaces (the id Note names), "" when none: the text answer
	// leads with a "now:" line for it (devco D-836).
	replaces string
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
		d.rows, lerr = loadRows(ctx, run, q, ms, keeps)
		return lerr
	})
	if err != nil {
		return Out{}, err
	}
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
var LensTables = []string{"resources", "kinds", "tasks", "row_meta", "knowledge_refs", "row_refs", "memories", "decisions", "rules", "patterns", "incidents", "hotfixes"}

// typedSQL reads the resources (with kind name, fields and history), the
// open tasks, their owed_by / owed_to metadata and their --ref pointers,
// in one statement: a first column says which a line is.
const typedSQL = `SELECT 'res', r.id, r.place, COALESCE(k.name, r.kind), r.name, r.fields, r.history, r.updated_at, r.aliases
FROM resources_all r LEFT JOIN kinds_all k ON k.id = r.kind WHERE r.archived_at IS NULL
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
	return statement == typedSQL || statement == textSQL || statement == daysSQL
}

// loadTyped reads the resources and the open tasks.
func loadTyped(ctx context.Context, run Query) ([]resourceRow, []taskRow, error) {
	res, err := run(ctx, typedSQL)
	if err != nil {
		return nil, nil, err
	}
	var rs []resourceRow
	var ts []taskRow
	at := map[string]int{}
	seenRes := map[string]bool{}
	type pending struct{ kind, id, a, b string }
	var later []pending
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
			r := resourceRow{ID: c[1], Place: c[2], Kind: c[3], Name: c[4], Fields: fieldsOf(c[5]), History: historyOf(c[6]), Updated: c[7]}
			if len(c) > 8 {
				r.Aliases = aliasesOf(c[8])
			}
			rs = append(rs, r)
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
// in name order.
func fieldsOf(s string) []fieldValue {
	if s == "" {
		return nil
	}
	var m map[string]struct {
		Name string          `json:"name"`
		V    json.RawMessage `json:"v"`
	}
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	out := make([]fieldValue, 0, len(m))
	for _, f := range m {
		out = append(out, fieldValue{Name: f.Name, Value: rawText(f.V)})
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
		for j := i; j > 0 && fs[j].Name < fs[j-1].Name; j-- {
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
// title, text, date and valid-until columns.
var textTables = []struct{ table, title, body, extra, until string }{
	{"memories", "''", "m.body", "m.valid_at", "m.valid_until"},
	{"decisions", "m.title", "m.body", "NULL", "NULL"},
	{"rules", "''", "m.body", "NULL", "NULL"},
	{"patterns", "m.title", "m.body", "NULL", "NULL"},
	{"incidents", "m.title", "m.body", "NULL", "NULL"},
	// devco D-815: a hotfix is the trap a coding question must not miss
	{"hotfixes", "m.title", "m.body", "NULL", "NULL"},
}

// Columns of a text statement line: kind ('row' or 'ref'), then for a row
// its table, id, place, title, text, date (COALESCE(extra, created_at)),
// imported, superseded-by, valid-until and the lowered "title text" the
// words are found in (NULL when the title or the text is NULL, which no
// word matches, as LIKE on NULL does not); for a ref its entity table,
// entity id and the resource id it references.
const (
	colKind, colTable, colID, colPlace, colTitle, colBody, colAt, colImported, colSuperseded, colUntil, colLower = 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10
	colRefID                                                                                                     = 3
	textCols                                                                                                     = 11
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
		if t.table != "incidents" {
			superseded = "COALESCE(m.superseded_by_id, '')"
			where = append(where, "m.archived_at IS NULL")
		}
		stmt := "SELECT 'row', '" + t.table + "', m.id, m.place, COALESCE(" + t.title + ", ''), COALESCE(" + t.body + ", ''), COALESCE(" + t.extra + ", m.created_at), " + imported + ", " + superseded + ", COALESCE(" + t.until + ", ''), lower(" + t.title + " || ' ' || " + t.body + ")" +
			" FROM " + t.table + "_all m"
		if len(where) > 0 {
			stmt += " WHERE " + strings.Join(where, " AND ")
		}
		parts = append(parts, stmt)
	}
	refs := "SELECT 'ref', entity_table, entity_id, ref_id, '', '', '', 0, '', '', NULL FROM row_refs_all"
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

// sqlLike is a LIKE pattern finding s anywhere (ESCAPE '\').
func sqlLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return sqlText("%" + r.Replace(strings.ToLower(s)) + "%")
}

// loadRows reads the rows that can answer q: rows holding a word of the
// question, a topic word or a matched resource's name, rows referencing a
// matched resource, and for a date question the rows of its window. A lens
// that keeps answers (keeps: the read server) reads textSQL, kept across
// calls, and the rows are picked here; any other runs one statement that
// picks them in SQL (one read: SQLite's scan is cheaper than handing every
// row over). rowFilter.answers decides as that statement does
// (TestTextRowsMatchTheLikeStatement compares the two), so both give the
// same rows in the same order (nearest store first).
func loadRows(ctx context.Context, run Query, q question, ms []match, keeps bool) ([]textRow, error) {
	var terms []string
	for _, m := range ms {
		terms = append(terms, m.res.Name)
		// a note that names it by an alias ("lunch with Bobby") is about it
		terms = append(terms, m.res.Aliases...)
	}
	if len(ms) == 0 {
		terms = append(terms, q.words...)
		for _, t := range q.topics {
			terms = append(terms, t.row...)
		}
	}
	f := rowFilter{pats: make([]string, 0, len(terms)), ids: map[string]bool{}, win: !q.window.IsZero()}
	for _, t := range terms {
		f.pats = append(f.pats, likeText(t))
	}
	var conds, refIDs []string
	for _, t := range terms {
		conds = append(conds, "lower(%T || ' ' || %B) LIKE "+sqlLike(t)+` ESCAPE '\'`)
	}
	for _, m := range ms {
		f.ids[m.res.ID] = true
		refIDs = append(refIDs, sqlText(m.res.ID))
	}
	if len(refIDs) > 0 {
		conds = append(conds, "m.id IN (SELECT entity_id FROM row_refs_all WHERE ref_id IN ("+strings.Join(refIDs, ", ")+"))")
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
		var err error
		if f.days, err = loadDays(ctx, run, f.near, keeps); err != nil {
			return nil, err
		}
		dayCond := "0"
		if dayIDs := f.inWindow(); len(dayIDs) > 0 {
			for i, id := range dayIDs {
				dayIDs[i] = sqlText(id)
			}
			dayCond = "m.id IN (" + strings.Join(dayIDs, ", ") + ")"
		}
		win := []string{"(%V >= " + sqlText(f.since) + " AND %V < " + sqlText(f.end) + ")", dayCond}
		if f.byText {
			conds = append(conds, win...)
		} else {
			conds = win
		}
	}
	if len(conds) == 0 {
		return nil, nil
	}
	stmt := textSQL
	if !keeps {
		if refIDs == nil {
			refIDs = []string{} // no ref lines: no resource matched
		}
		stmt = textStatement(strings.Join(conds, " OR "), refIDs)
	}
	res, err := run(ctx, stmt)
	if err != nil {
		return nil, err
	}
	refs := map[string][]string{}
	for _, row := range res.Rows {
		if len(row) < textCols || cell(row[colKind]) != "ref" || keeps && !f.ids[cell(row[colRefID])] {
			continue
		}
		ent := cell(row[colID])
		refs[ent] = append(refs[ent], cell(row[colRefID]))
	}
	f.refs = refs
	var out []textRow
	var stored []string // each row's date as stored, for metaDayOf
	at := map[string]int{}
	for _, row := range res.Rows {
		if len(row) < textCols || cell(row[colKind]) != "row" {
			continue
		}
		id := cell(row[colID])
		if keeps && !f.answers(row, id) {
			continue // a one-shot statement picked its rows in SQL
		}
		if _, dup := at[id]; dup {
			continue // nearest store first
		}
		at[id] = len(out)
		t, _ := time.Parse(time.RFC3339Nano, cell(row[colAt]))
		until, _ := time.Parse(time.RFC3339Nano, cell(row[colUntil]))
		stored = append(stored, cell(row[colAt]))
		out = append(out, textRow{Table: cell(row[colTable]), ID: id, Place: cell(row[colPlace]), Title: cell(row[colTitle]), Body: cell(row[colBody]), At: t, Imported: cell(row[colImported]) == "1", SupersededBy: cell(row[colSuperseded]), ValidUntil: until})
	}
	for i := range out {
		out[i].Refs = refs[out[i].ID]
		if f.win {
			out[i].Day = metaDayOf(q, f.days[out[i].ID], stored[i], f.near)
		}
	}
	return out, nil
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

// inWindow is the rows with a day metadata item inside [since, end),
// sorted: the rows the window holds by their day.
func (f *rowFilter) inWindow() []string {
	var ids []string
	for id, items := range f.days {
		if slices.ContainsFunc(items, dayRange{f.since, f.end}.holds) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
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
