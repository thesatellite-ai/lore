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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// StepMachineLocal is the step name the copy reports.
const StepMachineLocal = "machine_local"

// v017IDColumn is the id column of every v0.1.17 ent table.
const v017IDColumn = "id"

// v017TimeLayouts are the forms a v0.1.17 time column holds: modernc's
// text form of a Go time (what ent wrote), then RFC 3339.
// The last is Go's time.String form ("… +0000 UTC"), which some v0.1.17
// writes stored, monotonic reading (" m=+0.25") included; v017Time drops
// that reading first.
var v017TimeLayouts = []string{"2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00", time.RFC3339Nano, "2006-01-02 15:04:05.999999999 -0700 MST"}

// migrateMachineLocal copies the checkout's v0.1.17 machine-local rows
// into the machine store; see the file comment. It returns the step lines
// and the rows copied (under dryRun: the rows that would be).
//
// Only from this machine's own checkout of the repo (owner report
// 2026-11-12: `migrate run` in a copied checkout put its 50 render
// histories and 20 runs into ~/.lore/state): a copy's lore.db holds the
// original checkout's history, not this machine's. ownCheckout says when
// the checkout is a copy; then the rows are counted, named in a skipped
// line with why, and copied only with `--machine-local`.
func (a *App) migrateMachineLocal(ctx context.Context, dryRun bool) ([]string, int, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !st.eng.HasMachineStore() {
		return nil, 0, nil // no machine-local table in this build: nothing to copy
	}
	path := filepath.Join(st.repoTop, filepath.FromSlash(v017LoreDB))
	if !opsmigrate.MachineLocalAsked(ctx) {
		if why := a.notOwnCheckout(ctx, st); why != "" {
			counts, err := readMachineLocal(ctx, st.eng, path, true)
			if err != nil || len(counts) == 0 {
				return nil, 0, err
			}
			parts := make([]string, 0, len(counts))
			for _, c := range counts {
				if c.n > 0 {
					parts = append(parts, strconv.Itoa(c.n)+" "+string(c.table))
				}
			}
			if len(parts) == 0 {
				return nil, 0, nil
			}
			return []string{StepMachineLocal + ": skipped " + strings.Join(parts, ", ") + " rows of " + v017LoreDB + ": " + why +
				"; they stay in " + v017LoreDB + " (to copy them into this machine's store anyway: lore migrate run --machine-local)"}, 0, nil
		}
	}
	return copyMachineLocal(ctx, st.eng, path, dryRun)
}

// notOwnCheckout says why the checkout at st.repoTop is not this
// machine's checkout of its repo ("" when it is, or nothing tells):
//
//   - another root of the same repo id is in known_repos (every command
//     records where lore runs) and still holds a git repo: that one is
//     this machine's checkout, this one a copy;
//   - this machine's setups name folders (`lore setup list`) and none
//     holds the checkout: a scratch copy outside the person's code.
func (a *App) notOwnCheckout(ctx context.Context, st *state) string {
	top := resolvedPath(st.repoTop)
	if meta, ok, err := rowfs.ReadRepoMeta(st.repoTop); err == nil && ok && meta.RepoID != "" {
		if rows, lerr := st.eng.List(ctx, store.SourceMachine, schema.TableKnownRepos); lerr == nil {
			for _, r := range rows {
				root := fieldText(r, schema.FieldKnownRepoRoot)
				if r.Deleted || fieldText(r, schema.FieldKnownRepoID) != meta.RepoID || root == "" || root == top {
					continue
				}
				if _, serr := os.Stat(filepath.Join(root, ".git")); serr == nil {
					return "this checkout is a copy: " + root + " is this machine's checkout of the repo (" + meta.RepoID + ")"
				}
			}
		}
	}
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return "" // config errors are doctor's; nothing tells
	}
	enabled := cfg.Enabled()
	if !slices.ContainsFunc(enabled, func(e onboard.SetupEntry) bool { return len(e.Folders) > 0 }) {
		return ""
	}
	home, herr := os.UserHomeDir()
	if herr != nil {
		home = ""
	}
	if fr := onboard.MatchFolder(enabled, st.repoTop, home, resolveSetupPath); fr.Found || fr.Ambiguous() {
		return ""
	}
	return st.repoTop + " is in no folder of this machine's setups (`lore setup list` shows them), so it is taken for a copy"
}

// copyMachineLocal is the copy from the lore.db at path through eng.
func copyMachineLocal(ctx context.Context, eng *engine.Engine, path string, dryRun bool) ([]string, int, error) {
	counts, err := readMachineLocal(ctx, eng, path, dryRun)
	if err != nil {
		return nil, 0, err
	}
	var steps []string
	total := 0
	for _, c := range counts {
		if c.n > 0 {
			verb := "copied"
			if dryRun {
				verb = "would copy"
			}
			steps = append(steps, StepMachineLocal+": "+verb+" "+strconv.Itoa(c.n)+" "+string(c.table)+" rows from "+v017LoreDB+" into the machine store")
		}
		for _, sk := range c.skipped {
			if strings.HasPrefix(sk, noteFull) {
				steps = append(steps, StepMachineLocal+": "+strings.TrimPrefix(sk, noteFull))
				continue
			}
			steps = append(steps, StepMachineLocal+": skipped "+string(c.table)+" row "+sk)
		}
		total += c.n
	}
	return steps, total, nil
}

// machineCount is what the copy did (or would do) for one table.
type machineCount struct {
	table   row.Table
	n       int
	skipped []string
}

// readMachineLocal runs the copy (dryRun: counts only) table by table.
func readMachineLocal(ctx context.Context, eng *engine.Engine, path string, dryRun bool) ([]machineCount, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: stat "+v017LoreDB, err)
	}
	// read-only: v0.1.17 may still be using the file
	query := "mode=ro"
	if dryRun && git.NoClone(ctx) {
		query += "&immutable=1" // `lore fix --dry-run` writes nothing: no -shm/-wal either (B4-14)
	}
	dsn := fileuri.URL(path, query)
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: open "+v017LoreDB, err)
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to flush
	// item 42: every copied row is held in one stage and lands as ONE
	// machine-store write. Unstaged, each row's secret scan re-read the
	// scanner rules and the policy and allow rows of every attached store
	// (a full read of a 5,000-row repo folder, three times a row: 1,300
	// old runs and render histories took ~330 s of a 430 s lore fix); a
	// stage reads them once (stageScanned, settingsMemo). A stage the
	// caller already opened is used as it is.
	stage := ctx
	if !dryRun && !engine.InStage(ctx) {
		stage = engine.Staging(ctx)
		defer engine.Discard(stage) // after a Commit: nothing left to drop
	}
	if !dryRun {
		stage = engine.WithScanMemo(stage) // copyMachineTable prescans each table's rows on every CPU
	}
	var out []machineCount
	for _, def := range eng.Registry().Tables() {
		if !def.MachineLocal {
			continue
		}
		n, skipped, err := copyMachineTable(stage, eng, db, def, dryRun)
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "migrate: "+StepMachineLocal+" "+string(def.Name)+": "+err.Error(), err)
		}
		out = append(out, machineCount{table: def.Name, n: n, skipped: skipped})
	}
	if stage != ctx {
		// a redacted row's event is recorded here: no store-then-rotate
		// task for it (engine.WithSecretUnstored says why)
		if _, err := engine.Commit(engine.WithoutRedactTask(stage)); err != nil {
			return nil, errs.Prefix("migrate: "+StepMachineLocal+": write the copied rows into the machine store", err)
		}
	}
	return out, nil
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
	type toCopy struct {
		at     int // index in all: the skip notes stay in lore.db's row order
		id     ids.AnyID
		raw    string
		fields map[string]value.Value
	}
	var copies []toCopy
	notes := make([][]string, len(all))
	for at, r := range all {
		id, perr := ids.ParseAny(r.id)
		if perr != nil {
			r.bad = "id: " + perr.Error()
		} else if r.bad == "" {
			if _, verr := validate.Fields(def, r.fields, validate.Whole); verr != nil {
				r.bad = verr.Error()
			}
		}
		if r.bad != "" {
			notes[at] = append(notes[at], r.id+" ("+r.bad+")")
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
		copies = append(copies, toCopy{at: at, id: id, raw: r.id, fields: r.fields})
	}
	// B1-23: each create below scans its row's fields for secrets, one row
	// after another (render histories and run notes are big: ~170 s of a
	// first lore fix); the scans run on every CPU first (engine.Prescan)
	prescan := make([]map[string]value.Value, len(copies))
	for i, c := range copies {
		prescan[i] = c.fields
	}
	eng.Prescan(ctx, def.Name, prescan)
	for _, c := range copies {
		_, werr := eng.CreateWithID(ctx, store.SourceMachine, def.Name, c.id, c.fields)
		if secretFinding(werr) {
			// item 47: a secret-looking value in an old machine-local row
			// (runs.notes free text) is redacted, never a stop: the row
			// stays on this machine and the rest of the migration lands.
			found := findingsText(werr)
			if _, werr = eng.CreateWithID(eng.WithSecretUnstored(ctx), store.SourceMachine, def.Name, c.id, c.fields); werr == nil {
				n++
				notes[c.at] = append(notes[c.at], redactedNote(def.Name, c.raw, found))
				continue
			}
			if secretFinding(werr) {
				notes[c.at] = append(notes[c.at], secretSkipNote(def.Name, c.raw, werr))
				continue
			}
		}
		switch {
		case werr == nil:
			n++
		case errs.Has(werr, errs.Stale), errs.Has(werr, errs.Purged):
			// in the trash or purged on purpose since the last copy: the
			// person's later choice wins over the old row
		case errs.DefOf(errs.CodeOf(werr)).Class == errs.ClassBadInput:
			notes[c.at] = append(notes[c.at], c.raw+" ("+werr.Error()+")") // e.g. a reference or scan refusal
		default:
			return 0, nil, werr
		}
	}
	for _, ns := range notes {
		skipped = append(skipped, ns...)
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
	if i := strings.Index(s, " m="); i > 0 {
		s = s[:i]
	}
	for _, l := range v017TimeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return value.DateTimeFrom(t), nil
		}
	}
	return nil, errs.New(errs.InvalidInput, "not a time: "+s)
}

// noteFull marks a machineCount.skipped entry that is a whole step line
// of its own (a redacted or secret-skipped row), not "<id> (<why>)".
const noteFull = "\x00"

// secretFinding reports whether err is the secret scan's answer to a
// write (E_NEEDS_CONFIRM of on_detect ask, E_SECRET_DETECTED of refuse or
// of a value that cannot be redacted), not a confirmation of anything
// else: the scan's answers carry the masked findings.
func secretFinding(err error) bool {
	var e *errs.Error
	if !errors.As(err, &e) || (e.Code != errs.NeedsConfirm && e.Code != errs.SecretDetected) {
		return false
	}
	return len(e.Findings) > 0 || e.Code == errs.SecretDetected
}

// findingsText names err's findings: field, rule, line and the match
// hash (sha256:…), never any part of the value (item 29).
func findingsText(err error) string {
	var e *errs.Error
	if !errors.As(err, &e) || len(e.Findings) == 0 {
		return "a value that looks like a secret"
	}
	parts := make([]string, 0, len(e.Findings))
	for _, f := range e.Findings {
		parts = append(parts, "field "+f.Field+" ("+f.Rule+" at line "+strconv.Itoa(f.Line)+", "+f.Masked+")")
	}
	return strings.Join(parts, ", ")
}

// redactedNote is the step line of a row copied with its secret-looking
// matches replaced by ${secret:unstored} (item 47).
func redactedNote(table row.Table, id, found string) string {
	return noteFull + "redacted " + string(table) + " row " + id + ", " + found +
		": the match is stored as ${secret:unstored} in the machine store (this machine only); if it was a real key, rotate it" +
		" (to keep a vault reference there instead: lore secrets link " + id + " --at 1 --ref <manager>://<entry>)"
}

// secretSkipNote is the step line of a row whose finding cannot be
// redacted (a secret split across fields, a json list item): skipped,
// left in lore.db, the rest of the migration goes on (item 47).
func secretSkipNote(table row.Table, id string, err error) string {
	why := findingsText(err)
	if e := (*errs.Error)(nil); errors.As(err, &e) && len(e.Findings) == 0 {
		why = e.Message // e.g. "a secret is split across fields (<rule> across <a> and <b>)": rules and fields only
	}
	return noteFull + "skipped " + string(table) + " row " + id + ": " + why + " cannot be redacted (" + string(errs.CodeOf(err)) +
		"); the row stays in " + v017LoreDB + ". If the value is not a secret, turn its rule off (lore secrets rules lists them) and run lore fix again"
}
