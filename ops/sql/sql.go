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
// asks first (--yes / REST "yes"), exactly like `lore resource show`.
// Internal fields need no masking here: they are never written into a
// client repo (the leak guard), and a client repo's lens holds nothing else.
package sql

import (
	"context"
	"strconv"
	"strings"
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
}

// Env is what sql.run needs; *wire.App implements it.
//
// Invariant: SQL runs query on a read-only lens it opens and closes for
// this one call; with showSensitive false every sensitive value is masked
// and the lens refuses names that would read past the masked views.
type Env interface {
	SQL(ctx context.Context, query string, showSensitive bool) (Result, error)
}

// RunIn is the input of sql.run.
type RunIn struct {
	Query         string `json:"query" arg:"0" rest:"true" desc:"one SELECT, WITH or VALUES statement (quote it; several words are joined)"`
	ShowSensitive bool   `json:"show_sensitive,omitempty" flag:"show-sensitive" desc:"print sensitive values (asks first)"`
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
				errs.InvalidInput, errs.InvalidIdentifier, errs.ReadOnly, errs.NeedsConfirm, errs.Cancelled,
				errs.Unsupported, errs.DBNotFound, errs.NotAStore, errs.StoreUnreachable, errs.OldLore, errs.Internal,
			},
			Confirm: op.ConfirmRule{When: showSensitiveField},
			Examples: []op.Example{
				{Title: "count memories per store", CLI: `lore sql run "SELECT source, COUNT(*) AS n FROM memories_all GROUP BY source"`, Want: "source"},
			},
		}, func(ctx context.Context, in RunIn) (RunOut, error) { return run(ctx, env, in) }),
	}
}

// run checks the query and asks env.
func run(ctx context.Context, env Env, in RunIn) (RunOut, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return RunOut{}, errs.New(errs.InvalidInput, "sql: a query is required").
			WithHint(`lore sql run "SELECT name, ip FROM res_server"`)
	}
	res, err := env.SQL(ctx, q, in.ShowSensitive)
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
	return RunOut{Columns: cols, Rows: rows, Count: len(rows)}, nil
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
