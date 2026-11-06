package wire

// machinecopy.go — the one-time copy of a checkout's v0.1.17 machine-local
// rows into the machine store (P3-LOCAL-3), a step of `lore migrate run`.
//
//	migrateSettings ─▶ migrateMachineLocal(dryRun)
//	   no machine store attached / no .lore/lore.db ──▶ nothing
//	   every MachineLocal TableDef whose name is a lore.db table:
//	      SELECT id + the columns named like the def's fields (read-only)
//	      id already in the machine store (alive, in the trash, purged) ──▶ skip
//	      else CreateWithID(machine, table, the v0.1.17 id, typed fields)
//	   one step line per table that copied rows
//
// Generic on purpose: a table def that lands later (sessions, render
// histories, …) is copied with no new code, as long as its field names are
// the v0.1.17 column names (P3-RUN-1 keeps runs column for column). The
// v0.1.17 id is kept (same prefix + hex form), so a rerun, or a second
// checkout copying the same lore.db, writes nothing twice. Lossy on
// purpose: created_at/updated_at of the old row are not fields (the
// envelope is stamped by the engine at copy time); a row whose values do
// not fit the 0.1.18 table is skipped with a step line naming it and why
// (P3-LOCAL-4), never dropped silently and never blocking the rest.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// StepMachineLocal is the step name the copy reports.
const StepMachineLocal = "machine_local"

// v017IDColumn is the id column of every v0.1.17 ent table.
const v017IDColumn = "id"

// v017TimeLayouts are the forms a v0.1.17 time column holds: modernc's
// text form of a Go time (what ent wrote), then RFC 3339.
var v017TimeLayouts = []string{"2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00", time.RFC3339Nano}

// migrateMachineLocal copies the checkout's v0.1.17 machine-local rows
// into the machine store; see the file comment. It returns the step lines
// and the rows copied (under dryRun: the rows that would be).
func (a *App) migrateMachineLocal(ctx context.Context, dryRun bool) ([]string, int, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !st.eng.HasMachineStore() {
		return nil, 0, nil // no machine-local table in this build: nothing to copy
	}
	return copyMachineLocal(ctx, st.eng, filepath.Join(st.repoTop, filepath.FromSlash(v017LoreDB)), dryRun)
}

// copyMachineLocal is the copy from the lore.db at path through eng.
func copyMachineLocal(ctx context.Context, eng *engine.Engine, path string, dryRun bool) ([]string, int, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	} else if err != nil {
		return nil, 0, errs.Wrap(errs.Internal, "migrate: stat "+v017LoreDB, err)
	}
	// read-only: v0.1.17 may still be using the file
	dsn := fileuri.URL(path, "mode=ro")
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		return nil, 0, errs.Wrap(errs.Internal, "migrate: open "+v017LoreDB, err)
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to flush
	var steps []string
	total := 0
	for _, def := range eng.Registry().Tables() {
		if !def.MachineLocal {
			continue
		}
		n, skipped, err := copyMachineTable(ctx, eng, db, def, dryRun)
		if err != nil {
			return nil, 0, errs.Wrap(errs.CodeOf(err), "migrate: "+StepMachineLocal+" "+string(def.Name)+": "+err.Error(), err)
		}
		if n > 0 {
			verb := "copied"
			if dryRun {
				verb = "would copy"
			}
			steps = append(steps, StepMachineLocal+": "+verb+" "+strconv.Itoa(n)+" "+string(def.Name)+" rows from "+v017LoreDB+" into the machine store")
		}
		for _, sk := range skipped {
			steps = append(steps, StepMachineLocal+": skipped "+string(def.Name)+" row "+sk)
		}
		total += n
	}
	return steps, total, nil
}

// copyMachineTable copies one table; a table lore.db lacks copies nothing.
// skipped names each row that does not fit the 0.1.18 table ("<id>
// (<why>)"): v0.1.17 stored values 0.1.18 refuses (P3-RUN-1: any outcome
// text), and one such row must not block the whole migration, nor be
// dropped without a line (P3-LOCAL-4).
func copyMachineTable(ctx context.Context, eng *engine.Engine, db *sql.DB, def schema.TableDef, dryRun bool) (n int, skipped []string, err error) {
	cols, err := v017Columns(ctx, db, string(def.Name))
	if err != nil || len(cols) == 0 {
		return 0, nil, err
	}
	q, fields, err := v017SelectRows(def, cols)
	if err != nil {
		return 0, nil, err
	}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return 0, nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	type oldRow struct {
		id     string
		fields map[string]value.Value
		bad    string // why the row does not fit, "" when it does
	}
	var all []oldRow
	for rows.Next() {
		var doc string
		if err = rows.Scan(&doc); err != nil {
			return 0, nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
		}
		var cells map[string]json.RawMessage
		if err = json.Unmarshal([]byte(doc), &cells); err != nil {
			return 0, nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
		}
		var r oldRow
		if err = json.Unmarshal(cells[v017IDColumn], &r.id); err != nil {
			return 0, nil, errs.Wrap(errs.InvalidID, "a row id of "+v017LoreDB, err)
		}
		r.fields = map[string]value.Value{}
		for _, f := range fields {
			v, set, verr := v017Value(f, cells[f.Name])
			if verr != nil {
				r.bad = "column " + f.Name + ": " + verr.Error()
				break
			}
			if set {
				r.fields[f.Name] = v
			}
		}
		all = append(all, r)
	}
	if err = rows.Err(); err != nil {
		return 0, nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
	}
	for _, r := range all {
		id, perr := ids.ParseAny(r.id)
		if perr != nil {
			r.bad = "id: " + perr.Error()
		} else if r.bad == "" {
			if _, verr := validate.Fields(def, r.fields, validate.Whole); verr != nil {
				r.bad = verr.Error()
			}
		}
		if r.bad != "" {
			skipped = append(skipped, r.id+" ("+r.bad+")")
			continue
		}
		if _, _, gerr := eng.Get(ctx, engine.Ref{Table: def.Name, ID: id}); gerr == nil {
			continue // copied before (rerun)
		} else if !errs.Has(gerr, errs.NotFound) {
			return 0, nil, gerr
		}
		if dryRun {
			n++
			continue
		}
		_, werr := eng.CreateWithID(ctx, store.SourceMachine, def.Name, id, r.fields)
		switch {
		case werr == nil:
			n++
		case errs.Has(werr, errs.Stale), errs.Has(werr, errs.Purged):
			// in the trash or purged on purpose since the last copy: the
			// person's later choice wins over the old row
		case errs.DefOf(errs.CodeOf(werr)).Class == errs.ClassBadInput:
			skipped = append(skipped, r.id+" ("+werr.Error()+")") // e.g. a reference or scan refusal
		default:
			return 0, nil, werr
		}
	}
	return n, skipped, nil
}

// v017SelectRows is the statement reading def's rows out of lore.db as one
// JSON object per row (json_object, so every cell is typed JSON and no
// ...any Scan is needed), plus the fields it reads: the def's fields lore.db
// has a column for. SQLite binds no identifiers, so the table and column
// names are written into the statement: each is a TableDef name that must
// pass row.ValidName (snake_case) and was found in lore.db's own
// table_info, and is double-quoted; no row data reaches the statement.
func v017SelectRows(def schema.TableDef, cols map[string]bool) (string, []schema.FieldDef, error) {
	if !row.ValidName(string(def.Name)) {
		return "", nil, errs.New(errs.InvalidIdentifier, "migrate: table name "+string(def.Name))
	}
	var fields []schema.FieldDef
	pairs := []string{"'" + v017IDColumn + "', " + v017IDColumn}
	for _, f := range def.Fields {
		if !cols[f.Name] || f.Name == v017IDColumn {
			continue
		}
		if !row.ValidName(f.Name) {
			return "", nil, errs.New(errs.InvalidIdentifier, "migrate: column name "+f.Name)
		}
		fields = append(fields, f)
		pairs = append(pairs, "'"+f.Name+`', "`+f.Name+`"`)
	}
	return "SELECT json_object(" + strings.Join(pairs, ", ") + `) FROM "` + string(def.Name) + `" ORDER BY id`, fields, nil
}

// v017Columns returns the column names of lore.db's table (none when the
// table is missing).
func v017Columns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	cols := map[string]bool{}
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			return nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
		}
		cols[c] = true
	}
	if err = rows.Err(); err != nil {
		return nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
	}
	return cols, nil
}

// v017Value converts one v0.1.17 cell (JSON as json_object gave it) to a
// value of f's kind; set is false for NULL (the field stays unset). A list
// field reads ent's JSON array text (sessions.flags, outer_tools).
func v017Value(f schema.FieldDef, raw json.RawMessage) (v value.Value, set bool, err error) {
	k := f.Kind
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var s string
	if raw[0] != '"' {
		// a number cell: ent keeps bools as 0/1 and numbers as numbers
		if k == value.KindBool {
			b, perr := strconv.ParseBool(string(raw))
			return value.BoolOf(b), perr == nil, perr
		}
		if k != value.KindNumber {
			s = string(raw) // e.g. a numeric text column: keep its digits
		} else {
			v, err = value.Parse(k, raw)
			return v, err == nil, err
		}
	} else if err = json.Unmarshal(raw, &s); err != nil {
		return nil, false, err
	}
	switch k {
	case value.KindText:
		v, err = value.TextOf(s)
	case value.KindNumber:
		v, err = value.NumberOf(s)
	case value.KindBool:
		var b bool
		b, err = strconv.ParseBool(s)
		v = value.BoolOf(b)
	case value.KindDateTime:
		v, err = v017Time(s)
	case value.KindJSON:
		v, err = value.JSONOf([]byte(s))
	case value.KindList:
		v, err = value.ParseList(f.Elem, []byte(s))
	default:
		q, merr := json.Marshal(s)
		if merr != nil {
			return nil, false, merr
		}
		v, err = value.Parse(k, q)
	}
	return v, err == nil, err
}

// v017Time parses a v0.1.17 time text in any of v017TimeLayouts.
func v017Time(s string) (value.Value, error) {
	for _, l := range v017TimeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return value.DateTimeFrom(t), nil
		}
	}
	return nil, errs.New(errs.InvalidInput, "not a time: "+s)
}
