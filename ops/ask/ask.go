// Package ask defines `lore ask` (layer L5, devco D-807, gate G-3): one
// call that answers a question from every store the caller reads, so an
// agent recalls with one read instead of a search, a show per hit and a
// task list.
//
//	ask.run --query "<question>"
//	  ─▶ read the question: content words, topics (decision, status,
//	     budget …), a debt direction (what do I owe X / what does X owe
//	     me), a date window (2026-11-02, the week of …, yesterday, last
//	     week, two Fridays ago; against the clock)
//	  ─▶ one lens (Env.AskLens): resources with fields and history, open
//	     tasks with owed_by / owed_to and --ref pointers
//	  ─▶ the resources it names: by name, a typo of a name, a field value
//	     (a nickname, a role)
//	  ─▶ the rows: for a date the window's rows (journal days first); else
//	     the best rows about those resources and topics, newest first
//	  ─▶ capped text (--max-chars): resources with changed fields, open
//	     tasks, the top rows in full, the ids it cites
//
// It only reads: the lens is read-only and sensitive values are masked.
package ask

import (
	"context"
	"encoding/json"
	"strings"
	"time"

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
// several statements, all on the same lens.
type Env interface {
	AskLens(ctx context.Context, repoOnly bool, fn func(q Query) error) error
}

// RunIn is the input of ask.run.
type RunIn struct {
	Query string `json:"query" arg:"0" rest:"true" desc:"the question, in your own words: who is alice-ng, what is the status of orbit, what did we decide for orbit and why, what do I owe alice-ng, what does alice-ng owe me, what happened yesterday / last week / on 2026-11-02, everything about alice-ng (a typo, a nickname or a role finds the resource too)"`
	// Store names the stores to read, as every other read's --store.
	Store    string `json:"store,omitempty" flag:"store" alias:"to" desc:"read this store alone instead of every attached one: <org> (every folder), <store>/<folder>, a store's name, me (every folder of your personal store), or repo (the repo's own rows); works from any folder"`
	MaxChars int    `json:"max_chars,omitempty" flag:"max-chars" min:"200" desc:"cap the answer at this many characters (default 4000); what is left out is counted at the end"`
	Top      int    `json:"top,omitempty" flag:"top" min:"1" desc:"how many best rows to print in full (default 3); a date question prints every row of its dates"`
}

// Out is the output of ask.run: what answers the question, each item with
// its id and place, and the ids it cites.
type Out struct {
	Query string `json:"query" desc:"the question as asked"`
	When  string `json:"when,omitempty" desc:"the dates the question names, as read (2026-11-19 (yesterday))"`
	// Resources are the resources the question names, with their fields.
	Resources []Resource `json:"resources" desc:"the resources the question names (person, project, company …), each with its fields and the resources that link to it"`
	Tasks     []Task     `json:"tasks" desc:"the open tasks that answer it: for what do I owe X / what does X owe me every open one owed that way, else the open tasks of the resources it names"`
	Rows      []Row      `json:"rows" desc:"the rows that answer it, in full: the best ones newest first, or every row of the dates it names, oldest first"`
	// Ranked is the answer's reading order (devco D-818): the item that
	// answers the question type first, the named resource after it as
	// context. Why a separate list: the three typed lists keep their JSON
	// shape, while the order an agent reads (and the text prints) changes
	// with the question: a status or decision row before its project, the
	// owed tasks before the person, the newest rows of each folder right
	// after the person for everything about X.
	Ranked   []Ranked `json:"ranked" desc:"every item of the answer in reading order, the one that answers the question first: a status or decision row before its project, the owed tasks before the person, the person then the newest row of each folder for everything about X"`
	Cited    []string `json:"cited" desc:"the id of every item in the answer, in reading order"`
	Left     int      `json:"left,omitempty" desc:"how many items were left out to stay under --max-chars"`
	maxChars int
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
	ID     string   `json:"id"`
	Kind   string   `json:"kind" desc:"its kind (person, project, company …)"`
	Name   string   `json:"name"`
	Place  string   `json:"place" desc:"where it is stored (<store>/<folder>)"`
	Match  string   `json:"match" desc:"how the question named it: name, typo (a name a letter or two off) or field (a nickname, a role)"`
	Fields []Field  `json:"fields" desc:"its field values (sensitive ones masked)"`
	Linked []Linked `json:"linked" desc:"the resources whose fields name it (the people of a company, the projects a person owns)"`
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
	err := env.AskLens(ctx, in.Store == string(store.SourceRepo), func(run Query) error {
		var lerr error
		if d.resources, d.tasks, lerr = loadTyped(ctx, run); lerr != nil {
			return lerr
		}
		ms := matchResources(q, d.resources)
		if q.owe != oweNone || (isIdentity(q.text) && len(ms) > 0) {
			return nil // answered by resources and tasks
		}
		d.rows, lerr = loadRows(ctx, run, q, ms)
		return lerr
	})
	if err != nil {
		return Out{}, err
	}
	return build(q, d, in.Top, in.MaxChars), nil
}

// cell is a lens cell as text ("" for NULL).
func cell(v value.Value) string {
	if v == nil {
		return ""
	}
	return v.String()
}

// sqlText quotes s as an SQL string literal.
func sqlText(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// sqlLike is a LIKE pattern finding s anywhere (ESCAPE '\').
func sqlLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return sqlText("%" + r.Replace(strings.ToLower(s)) + "%")
}

// LensTables are the tables the statements of ask read (each through its
// "<table>_all" view): the lens is built with these alone (devco D-813),
// so a call does not read and view the other tables of every cache.
// TestLensTablesCoverTheStatements fails when a statement reads another.
var LensTables = []string{"resources", "kinds", "tasks", "row_meta", "knowledge_refs", "row_refs", "memories", "decisions", "rules", "patterns", "incidents"}

// typedSQL reads the resources (with kind name, fields and history), the
// open tasks, their owed_by / owed_to metadata and their --ref pointers,
// in one statement: a first column says which a line is.
const typedSQL = `SELECT 'res', r.id, r.place, COALESCE(k.name, r.kind), r.name, r.fields, r.history, r.updated_at
FROM resources_all r LEFT JOIN kinds_all k ON k.id = r.kind WHERE r.archived_at IS NULL
UNION ALL SELECT 'tsk', t.id, t.place, t.status, t.title, '', '', t.created_at
FROM tasks_all t WHERE t.status NOT IN ('done', 'cancelled')
UNION ALL SELECT 'met', m.entity_id, '', m.key_name, m.value_text, '', '', ''
FROM row_meta_all m WHERE m.entity_table = 'tasks' AND m.item_idx >= 0 AND m.key_name IN ('owed_by', 'owed_to')
UNION ALL SELECT 'ref', kr.src_id, '', kr.relation, kr.dst_id, '', '', ''
FROM knowledge_refs_all kr WHERE kr.src_table = 'tasks' AND kr.dst_table = 'resources'`

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
			rs = append(rs, resourceRow{ID: c[1], Place: c[2], Kind: c[3], Name: c[4], Fields: fieldsOf(c[5]), History: historyOf(c[6]), Updated: c[7]})
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

// textTables are the tables ask reads rows from, with the SQL of their
// title and text.
// loadDays reads the rows whose day metadata falls in [since, end) (days
// as YYYY-MM-DD): row id → its day, and the ids quoted for an IN list.
// Read once on its own: row_meta_all reads every row's metadata JSON, and
// as a subquery of each text table's arm it ran once per table (devco
// D-813: a date question cost ~350 ms of its ~700 ms there).
func loadDays(ctx context.Context, run Query, since, end string) (map[string]string, []string, error) {
	res, err := run(ctx, "SELECT entity_id, value_text FROM row_meta_all WHERE key_name = 'day' AND item_idx >= 0 AND value_text >= "+sqlText(since)+" AND value_text < "+sqlText(end))
	if err != nil {
		return nil, nil, err
	}
	days := map[string]string{}
	var ids []string
	for _, row := range res.Rows {
		if len(row) < 2 {
			continue
		}
		id := cell(row[0])
		if _, seen := days[id]; !seen {
			ids = append(ids, sqlText(id))
		}
		days[id] = cell(row[1])
	}
	return days, ids, nil
}

var textTables = []struct{ table, title, body, extra string }{
	{"memories", "''", "m.body", "m.valid_at"},
	{"decisions", "m.title", "m.body", "NULL"},
	{"rules", "''", "m.body", "NULL"},
	{"patterns", "m.title", "m.body", "NULL"},
	{"incidents", "m.title", "m.body", "NULL"},
}

// loadRows reads the rows that can answer q: rows holding a word of the
// question, a topic word or a matched resource's name, rows referencing a
// matched resource, and for a date question the rows of its window.
func loadRows(ctx context.Context, run Query, q question, ms []match) ([]textRow, error) {
	var terms []string
	for _, m := range ms {
		terms = append(terms, m.res.Name)
	}
	if len(ms) == 0 {
		terms = append(terms, q.words...)
		for _, t := range q.topics {
			terms = append(terms, t.row...)
		}
	}
	var ids []string
	for _, m := range ms {
		ids = append(ids, sqlText(m.res.ID))
	}
	var conds []string
	for _, t := range terms {
		conds = append(conds, "lower(%T || ' ' || %B) LIKE "+sqlLike(t)+` ESCAPE '\'`)
	}
	if len(ids) > 0 {
		conds = append(conds, "m.id IN (SELECT entity_id FROM row_refs_all WHERE ref_id IN ("+strings.Join(ids, ", ")+"))")
	}
	win := !q.window.IsZero()
	var days map[string]string
	if win {
		// days as YYYY-MM-DD: a stored date (2026-11-02T00:00:00.000000000Z)
		// sorts after its day and before the next one
		since, end := q.window.Since.Format(time.DateOnly), q.window.End.Format(time.DateOnly)
		var dayIDs []string
		var err error
		if days, dayIDs, err = loadDays(ctx, run, since, end); err != nil {
			return nil, err
		}
		dayCond := "0"
		if len(dayIDs) > 0 {
			dayCond = "m.id IN (" + strings.Join(dayIDs, ", ") + ")"
		}
		conds = append(conds, "(%V >= "+sqlText(since)+" AND %V < "+sqlText(end)+")", dayCond)
		if len(ms) > 0 {
			conds = conds[len(conds)-2:] // the window decides; the resource filter runs in Go
		}
	}
	if len(conds) == 0 {
		return nil, nil
	}
	var parts []string
	for _, t := range textTables {
		where := strings.NewReplacer("%T", t.title, "%B", t.body, "%V", "COALESCE("+t.extra+", m.created_at)").Replace(strings.Join(conds, " OR "))
		imported := "0"
		if t.table == "memories" {
			imported = "(m.source_kind = 'imported')"
		}
		superseded := "0"
		if t.table != "incidents" {
			superseded = "(m.superseded_by_id IS NOT NULL AND m.superseded_by_id != '')"
		}
		archived := ""
		if t.table != "incidents" {
			archived = " AND m.archived_at IS NULL"
		}
		parts = append(parts, "SELECT 'row', '"+t.table+"', m.id, m.place, COALESCE("+t.title+", ''), COALESCE("+t.body+", ''), COALESCE("+t.extra+", m.created_at), "+imported+", "+superseded+
			" FROM "+t.table+"_all m WHERE ("+where+")"+archived)
	}
	stmt := strings.Join(parts, "\nUNION ALL ")
	if len(ids) > 0 {
		stmt += "\nUNION ALL SELECT 'ref', entity_table, entity_id, ref_id, '', '', '', 0, 0 FROM row_refs_all WHERE ref_id IN (" + strings.Join(ids, ", ") + ")"
	}
	res, err := run(ctx, stmt)
	if err != nil {
		return nil, err
	}
	var out []textRow
	at := map[string]int{}
	refs := map[string][]string{}
	for _, row := range res.Rows {
		if len(row) < 9 {
			continue
		}
		c := make([]string, len(row))
		for i, v := range row {
			c[i] = cell(v)
		}
		switch c[0] {
		case "row":
			if _, dup := at[c[2]]; dup {
				continue // nearest store first
			}
			at[c[2]] = len(out)
			t, _ := time.Parse(time.RFC3339Nano, c[6])
			out = append(out, textRow{Table: c[1], ID: c[2], Place: c[3], Title: c[4], Body: c[5], At: t, Imported: c[7] == "1", Superseded: c[8] == "1"})
		case "ref":
			refs[c[2]] = append(refs[c[2]], c[3])
		}
	}
	for i := range out {
		out[i].Refs = refs[out[i].ID]
		out[i].Day = days[out[i].ID]
	}
	return out, nil
}
