// Package sql defines `lore sql` (layer L5): one general, read-only SQL
// query over every table of the repo's cache lens (decision MQ6, spec 7.4,
// spec 15 step 5: `lore sql "SELECT name, ip FROM res_server"`).
//
// What the query sees is the lens wire builds: a `<table>_all` view per
// table with a leading `source` column (the store's name), plus each table
// by its plain name (the nearest store's copy). In a client repo the lens
// holds only the repo's own cache, never a store's data (MQ6, CFG6).
//
// Two independent locks keep it read-only, and both stay: the lens refuses
// any statement that is not one SELECT / WITH / VALUES before it runs, and
// its connection is query_only over files attached mode=ro.
//
// Sensitive kind fields print as ••• (RQ1) unless --show-sensitive, which
// asks first (--yes / REST "yes"), exactly like `lore resource show`
// (XQ3: without a terminal it needs --yes, else E_NEEDS_CONFIRM).
// Internal fields need no masking here: they are never written into a
// client repo (the leak guard), and a client repo's lens holds nothing else.
//
// Client repos (CFG4, META 3.16 step 5): the lens holds the repo's cache
// plus the stores marked visibility: shareable; --internal adds the
// internal stores, and the output names them (RunOut.Internal and a note
// under the table) so their rows read as internal (FA-META-1).
package sql

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore sql run`, REST POST /v1/sql/run).
const (
	Noun  = "sql"
	OpRun = "sql.run"
)

// restNoun keeps the REST collection "sql" (the default plural would be
// "sqls").
const restNoun = "sql"

// showSensitiveField is the json name of RunIn.ShowSensitive, the input
// that makes sql.run ask first (op.ConfirmRule.When).
const showSensitiveField = "show_sensitive"

// Result is one query's answer: the column names in select order and the
// rows, every cell a typed value (integer and real → number, text → text,
// NULL → null).
type Result struct {
	Columns []string
	Rows    [][]value.Value
	// Internal names the internal stores the lens attached only because of
	// --internal in a client repo (CFG4); empty otherwise.
	Internal []string
}

// Request is one query as sql.run hands it to Env.
type Request struct {
	Query         string
	ShowSensitive bool
	// Timeout bounds the statement itself, not the lens open (the refresh
	// before it can fetch); always > 0 here (DefaultTimeout when unset).
	Timeout time.Duration
	// Internal also attaches the internal stores in a client repo (CFG4);
	// in an own repo every store is attached already.
	Internal bool
	// IncludeDeleted adds the rows in the trash of the attached stores to
	// every table view (DELETION 2.9, FA-META-2).
	IncludeDeleted bool
}

// Env is what sql.run needs; *wire.App implements it.
//
// Invariant: SQL runs the query on a read-only lens it opens and closes for
// this one call; with ShowSensitive false every sensitive value is masked
// and the lens refuses names that would read past the masked views. A
// statement past Timeout is E_SQL_TIMEOUT, a refused one E_SQL_REFUSED.
type Env interface {
	SQL(ctx context.Context, r Request) (Result, error)
}

// DefaultTimeout is --timeout when unset (META 3.16: "the query ran longer
// than 10s").
const DefaultTimeout = 10 * time.Second

// Output formats of --format (RES 3.10, META 3.16, R7). --json stays the
// typed machine form; --format json is a plain array of row objects.
const (
	FormatTable = "table"
	FormatCSV   = "csv"
	FormatJSON  = "json"
)

// Formats lists the --format words.
func Formats() []string { return []string{FormatTable, FormatCSV, FormatJSON} }

// RunIn is the input of sql.run.
type RunIn struct {
	Query         string        `json:"query" arg:"0" rest:"true" desc:"one SELECT, WITH or VALUES statement (quote it; several words are joined)"`
	ShowSensitive bool          `json:"show_sensitive,omitempty" flag:"show-sensitive" desc:"print sensitive values (asks first)"`
	Format        string        `json:"format,omitempty" flag:"format" desc:"table (default), csv or json (an array of row objects)"`
	Timeout       time.Duration `json:"timeout,omitempty" flag:"timeout" desc:"stop the statement after this long (default 10s); past it E_SQL_TIMEOUT"`
	Internal      bool          `json:"internal,omitempty" flag:"internal" desc:"in a client repo, also read the internal stores (their rows are internal: never copy them into the repo)"`
	// IncludeDeleted: DELETION 2.9 (FA-META-2).
	IncludeDeleted bool `json:"include_deleted,omitempty" flag:"include-deleted" desc:"also read the rows in the trash (tables and their _all views; time_spent counts deleted tasks)"`
}

// RunOut is the output of sql.run (--json and REST): column names, rows
// of typed cells, and the row count.
//
// Why cells are {kind, value} objects and not bare JSON values: every op
// output is described field by field (OpenAPI, the CLI reference), and that
// description has no "any JSON value" type, so a row of mixed numbers,
// text and NULLs is spelled as a list of typed cells the schema can state
// truthfully.
type RunOut struct {
	Columns []string `json:"columns"`
	Rows    []Row    `json:"rows"`
	Count   int      `json:"count"`
	// Internal lists the internal stores read only because of --internal in
	// a client repo; their rows are internal (CFG4).
	Internal []string `json:"internal,omitempty"`
	// Format is RunIn.Format for Text; not part of the --json shape.
	Format string `json:"-"`
}

// Row is one result row, its cells in Columns order.
type Row struct {
	Cells []Cell `json:"cells"`
}

// Cell is one typed value: Kind is the value kind ("number", "text",
// "null"; model/value's names) and Value its text form ("" for null;
// a number as SQLite printed it, so 2.5 and 10 keep their spelling).
type Cell struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// cellOf converts one lens value.
func cellOf(v value.Value) Cell {
	if v == nil || v.Kind() == value.KindNull {
		return Cell{Kind: string(value.KindNull)}
	}
	return Cell{Kind: string(v.Kind()), Value: v.String()}
}

// Ops returns the sql operation bound to env.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Run one read-only SQL query over every table of the repo's lens (sensitive values masked)",
			Permission: op.PermRead, Access: op.AccessRead, RESTNoun: restNoun,
			Errors: []errs.Code{
				errs.InvalidInput, errs.InvalidIdentifier, errs.SQLRefused, errs.SQLTimeout, errs.NeedsConfirm, errs.Cancelled,
				errs.Unsupported, errs.DBNotFound, errs.NotAStore, errs.StoreUnreachable, errs.OldLore, errs.Internal,
			},
			Confirm: op.ConfirmRule{When: showSensitiveField},
			Examples: []op.Example{
				{Title: "count memories per store", CLI: `lore sql run --query "SELECT source, COUNT(*) AS n FROM memories_all GROUP BY source"`, Want: "source"},
				{Title: "every resource as csv", CLI: `lore sql run --query "SELECT kind, name FROM res__all ORDER BY kind, name" --format csv`, Want: "kind,name"},
			},
		}, func(ctx context.Context, in RunIn) (RunOut, error) { return run(ctx, env, in) }),
	}
}

// run checks the input and asks env:
//
//	query trimmed, non-empty ─▶ --format known ─▶ --timeout (default 10s)
//	  ─▶ env.SQL (lens: refuse writes E_SQL_REFUSED, stop at timeout E_SQL_TIMEOUT)
//	  ─▶ typed cells ─▶ Text prints table | csv | json (R7)
func run(ctx context.Context, env Env, in RunIn) (RunOut, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return RunOut{}, errs.New(errs.InvalidInput, "sql: a query is required").
			WithHint(`lore sql run --query "SELECT name, ip FROM res_server"`)
	}
	format := in.Format
	if format == "" {
		format = FormatTable
	}
	if !slices.Contains(Formats(), format) {
		return RunOut{}, errs.New(errs.InvalidInput, "sql: unknown --format "+strconv.Quote(in.Format)).
			WithHint("one of " + strings.Join(Formats(), ", "))
	}
	if in.Timeout < 0 {
		return RunOut{}, errs.New(errs.InvalidInput, "sql: --timeout must be positive")
	}
	timeout := in.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	res, err := env.SQL(ctx, Request{Query: q, ShowSensitive: in.ShowSensitive, Timeout: timeout, Internal: in.Internal, IncludeDeleted: in.IncludeDeleted})
	if err != nil {
		return RunOut{}, err
	}
	rows := make([]Row, len(res.Rows))
	for i, r := range res.Rows {
		cells := make([]Cell, len(r))
		for j, v := range r {
			cells[j] = cellOf(v)
		}
		rows[i] = Row{Cells: cells}
	}
	cols := res.Columns
	if cols == nil {
		cols = []string{}
	}
	return RunOut{Columns: cols, Rows: rows, Count: len(rows), Internal: res.Internal, Format: format}, nil
}

// internalNote is the line under the table of a query that read internal stores
// (CFG4: the output marks their rows internal).
func (o RunOut) internalNote() string {
	if len(o.Internal) == 0 {
		return ""
	}
	return "note: internal stores read (--internal): " + strings.Join(o.Internal, ", ") +
		"; rows with these sources are internal, never copy them into this client repo\n"
}

// Human-form constants.
const (
	// colGap separates two columns of the table.
	colGap = "  "
	// nullText is how a NULL cell prints (sqlite3's .nullvalue is empty;
	// an explicit word keeps NULL apart from an empty string).
	nullText = "NULL"
	// maxCellRunes caps one cell so a long body cannot push every other
	// column off screen; --json carries the whole value.
	maxCellRunes = 60
	// cutKeepRunes is how much of a capped cell is kept before the ellipsis.
	cutKeepRunes = maxCellRunes - 1
	// ellipsis ends a capped cell.
	ellipsis = "…"
	// noRows is the line of an empty result.
	noRows = "(no rows)"
)

// Text is an aligned table: a header of the column names, one line per
// row, every column padded to its widest cell (by characters), then a
// "(N rows)" line. A cell's line breaks and tabs print as spaces so a row
// stays one line.
func (o RunOut) Text() string {
	switch o.Format {
	case FormatCSV:
		return o.csvText()
	case FormatJSON:
		return o.jsonText()
	}
	return o.tableText() + o.internalNote()
}

// tableText is the aligned table of Text.
func (o RunOut) tableText() string {
	if len(o.Rows) == 0 {
		return strings.Join(o.Columns, colGap) + "\n" + noRows + "\n"
	}
	cells := make([][]string, 0, len(o.Rows)+1)
	cells = append(cells, o.Columns)
	for _, r := range o.Rows {
		line := make([]string, len(r.Cells))
		for i, c := range r.Cells {
			line[i] = cellText(c)
		}
		cells = append(cells, line)
	}
	widths := make([]int, len(o.Columns))
	for _, line := range cells {
		for i, c := range line {
			if i < len(widths) {
				widths[i] = max(widths[i], utf8.RuneCountInString(c))
			}
		}
	}
	var b strings.Builder
	for _, line := range cells {
		for i, c := range line {
			if i > 0 {
				b.WriteString(colGap)
			}
			b.WriteString(c)
			if i < len(line)-1 && i < len(widths) {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)))
			}
		}
		b.WriteString("\n")
	}
	b.WriteString(rowCount(len(o.Rows)) + "\n")
	return b.String()
}

// rowCount is "(1 row)" / "(N rows)".
func rowCount(n int) string {
	if n == 1 {
		return "(1 row)"
	}
	return "(" + strconv.Itoa(n) + " rows)"
}

// cellText is one cell's human form: NULL, or the value's text on one
// line, capped at maxCellRunes characters.
func cellText(c Cell) string {
	if c.Kind == string(value.KindNull) {
		return nullText
	}
	s := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, c.Value)
	if utf8.RuneCountInString(s) > maxCellRunes {
		s = string([]rune(s)[:cutKeepRunes]) + ellipsis
	}
	return s
}

// csvText is RFC 4180 csv: a header of the column names, then one record
// per row; NULL is an empty field, values are whole (no capping).
func (o RunOut) csvText() string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	// strings.Builder writes never fail, so Write's error is always nil.
	_ = w.Write(o.Columns)
	for _, r := range o.Rows {
		rec := make([]string, len(r.Cells))
		for i, c := range r.Cells {
			rec[i] = c.Value
		}
		_ = w.Write(rec)
	}
	w.Flush()
	return b.String()
}

// jsonText is one JSON array of row objects keyed by column name, in
// column order: numbers as JSON numbers, text as strings, NULL as null.
// Built by hand so the cells stay typed without an "any" map.
func (o RunOut) jsonText() string {
	var b strings.Builder
	b.WriteString("[")
	for ri, r := range o.Rows {
		if ri > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n  {")
		for i, c := range r.Cells {
			if i >= len(o.Columns) {
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(jsonString(o.Columns[i]) + ": " + jsonCell(c))
		}
		b.WriteString("}")
	}
	if len(o.Rows) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("]\n")
	return b.String()
}

// jsonCell is one cell as a JSON value. A number SQLite printed that is not
// a finite JSON number (Inf, NaN) prints as a string rather than break the
// document.
func jsonCell(c Cell) string {
	switch c.Kind {
	case string(value.KindNull):
		return "null"
	case string(value.KindNumber):
		if f, err := strconv.ParseFloat(c.Value, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && json.Valid([]byte(c.Value)) {
			return c.Value
		}
	}
	return jsonString(c.Value)
}

// jsonString is s as a JSON string (encoding a string cannot fail).
func jsonString(s string) string {
	out, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(out)
}
