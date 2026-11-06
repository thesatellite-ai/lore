package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	modernc "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// wantAttached is the attach limit spec 7.4 asks for.
const wantAttached = 125

// MaxLensSources is the most cache files one lens can attach: the
// SQLITE_MAX_ATTACHED modernc.org/sqlite is compiled with. sqlite3_limit
// cannot raise a limit past it (TestAttachLimit proves the number), so an
// attach lens over more sources is refused (E_UNSUPPORTED) rather than
// silently truncated; past the limit the caller uses one combined cache
// file for every source and OpenLensCombined (CFG3 fallback, O10).
const MaxLensSources = 10

// LensSource is one cache file in a lens. Name becomes the `source` column
// (the store's name in stores.yaml, or store.SourceRepo).
//
// Combined marks a combined cache file (CFG3, O10) holding several
// sources: its rows take `source` from their own _source column and
// ResolveID reports each row's own source, so Name is only a label there.
// A lens may mix plain files and combined ones (the repo's own cache plus
// the combined file of its stores).
//
// Deleted marks the one file of trashed rows a lens built for `lore sql
// --include-deleted` holds (DELETION 2.9, FA-META-2): each row applied
// under its store's name, so like a combined file it takes `source` from
// _source. Its rows join every "<table>_all" view, and the plain "<table>"
// view of the nearest store gains that store's trashed rows.
type LensSource struct {
	Name     string
	Path     string
	Combined bool
	Deleted  bool
}

// ownSource reports whether the file's rows carry their own source name
// in _source (a combined or a deleted-rows file).
func (s LensSource) ownSource() bool { return s.Combined || s.Deleted }

// LensOptions changes what a lens exposes.
type LensOptions struct {
	// MaskSensitive replaces every value of a sensitive kind field with
	// MaskedValue in the lens views (RQ1: sensitive values are hidden from
	// sql output unless asked for) and refuses any query that could read
	// past the views (see lens_mask.go).
	MaskSensitive bool
	// Expand backs the SQL function lore_expand(text) (SECRETS_AND_REFERENCES
	// 12.1, SQ4): var, res and row references filled in, never env or a
	// secret (SQ15, BATCH 10), never a sensitive value. nil leaves the text
	// as stored.
	Expand Expander
	// Trash is the rows in the trash the `trash` view lists (DELETION 3.2,
	// lens_trash.go): the cache keeps no trashed row, so the caller reads
	// them from the stores and hands them in. nil gives an empty view.
	Trash []TrashRow
	// TimeClientKey is the metadata key (a name, or its mky_ id) the
	// time_spent view reads as the client column: the policy
	// time.client_key (TQ8, FB-TASK-4). "" means TimeClientKey ("client").
	TimeClientKey string
}

// LensHit is where ResolveID found an id.
type LensHit struct {
	Source string
	Table  row.Table
}

// Result is what Query returns: column names and typed rows, every cell
// narrowed to a value.Value (integer and real → Number, text → Text,
// NULL → Null).
type Result struct {
	Columns []string
	Rows    [][]value.Value
}

// Lens is a read-only, in-memory connection over several cache files
// (spec 7.4), built fresh for one command.
//
// Invariant: the connection is pinned (one *sql.Conn), every file is
// attached with mode=ro, and PRAGMA query_only is on once the views exist,
// so nothing reachable through it can write.
type Lens struct {
	mu      sync.Mutex
	db      *sql.DB
	conn    *sql.Conn
	sources []LensSource
	sql     []sourceSQL // per source, same order
	views   []string
	// masked is LensOptions.MaskSensitive: the views mask sensitive values
	// and Query refuses the names that would bypass them (hidden).
	masked bool
	hidden []string
	closed bool
	expand Expander
	trash  []TrashRow
	// clientKey is LensOptions.TimeClientKey (timespans.go).
	clientKey string
}

// attachedName is the schema name of the i-th source. Store names are
// never used as SQL identifiers; they appear only as quoted literals.
func attachedName(i int) string { return "s_" + strconv.Itoa(i) }

// sourceSQL is every fixed statement the lens runs against one attached
// source, built once from the validated schema name.
type sourceSQL struct {
	attach  string // takes the file URI
	objects string // the `_tables` records
	resolve string // takes an id: (source, table) of every live row with that id
	// sensitive lists (kind id, kind name, field id, field name) of every
	// sensitive kind field (lens_mask.go).
	sensitive string
	// metaSensitive lists the meta key ids its meta_keys marks sensitive.
	metaSensitive string
}

// sqlForSource builds the statements of the i-th source.
func sqlForSource(i int) (sourceSQL, error) {
	sq, err := ident(attachedName(i))
	if err != nil {
		return sourceSQL{}, err
	}
	return sourceSQL{
		attach:  "ATTACH DATABASE ? AS " + sq,
		objects: "SELECT objects FROM " + sq + `."` + tblTables + `" ORDER BY name`,
		resolve: "SELECT source, tbl FROM " + sq + `."` + tblRows + `" WHERE id = ? AND deleted = 0 ORDER BY source, tbl`,
		sensitive: `SELECT k.id, k."` + fieldName + `", f.id, f."` + fieldName + `" FROM ` + sq + `."` + schema.TableKindFields + `" f JOIN ` +
			sq + `."` + schema.TableKinds + `" k ON k.id = f."` + fieldKindID + `" WHERE f."` + fieldSensitive + `" = 1`,
		metaSensitive: `SELECT DISTINCT id FROM ` + sq + `."` + schema.TableMetaKeys + `" WHERE "` + fieldSensitive + `" = 1`,
	}, nil
}

// OpenLens attaches every source read-only and creates TEMP VIEW
// "<table>_all" (leading `source` column, UNION ALL over the sources that
// have the table). Tables whose columns differ between sources (two lore
// versions, two shapes of one kind) are unioned by column name, NULL where
// a source lacks a column.
func OpenLens(ctx context.Context, sources []LensSource) (*Lens, error) {
	return OpenLensWith(ctx, sources, LensOptions{})
}

// OpenLensWith is OpenLens with options (masking of sensitive values).
func OpenLensWith(ctx context.Context, sources []LensSource, o LensOptions) (*Lens, error) {
	if err := checkSources(sources); err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, "file::memory:")
	if err != nil {
		return nil, dbErr("open lens", err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(dbErr("open lens", err), db.Close())
	}
	l := &Lens{db: db, conn: conn, sources: slices.Clone(sources), masked: o.MaskSensitive, expand: o.Expand, trash: o.Trash, clientKey: o.TimeClientKey}
	if err := l.setup(ctx); err != nil {
		return nil, errors.Join(err, l.Close())
	}
	return l, nil
}

// combinedSourceName is the LensSource name of the one file of a combined
// lens; it never reaches a result (the `source` column comes from each
// row's _source there).
const combinedSourceName = "combined"

// OpenLensCombined opens a lens over one combined cache file that holds
// every source (CFG3 fallback past MaxLensSources, decision O10): the file
// a Cache opened with Open fills by applying each store under its own
// store.Source. Views are the same "<table>_all" as OpenLens, with
// `source` = each row's _source (so wire should apply every store under
// the name it would give it as a LensSource). Tables without a _source
// column (row_refs, row_expanded) get a NULL source. ResolveID reports an
// id held by two sources of the file as E_AMBIGUOUS_ID.
func OpenLensCombined(ctx context.Context, path string) (*Lens, error) {
	if path == "" {
		return nil, errs.New(errs.InvalidInput, "sqlite lens: a combined lens needs a path")
	}
	return OpenLens(ctx, []LensSource{{Name: combinedSourceName, Path: path, Combined: true}})
}

// checkSources validates the source list.
func checkSources(sources []LensSource) error {
	if len(sources) == 0 {
		return errs.New(errs.InvalidInput, "sqlite lens: no sources")
	}
	if len(sources) > MaxLensSources {
		return errs.New(errs.Unsupported, "sqlite lens: "+strconv.Itoa(len(sources))+" sources, at most "+
			strconv.Itoa(MaxLensSources)+" can be attached (modernc.org/sqlite SQLITE_MAX_ATTACHED)")
	}
	seen := map[string]bool{}
	for _, s := range sources {
		if s.Name == "" || s.Path == "" {
			return errs.New(errs.InvalidInput, "sqlite lens: a source needs a name and a path")
		}
		if seen[s.Name] {
			return errs.New(errs.InvalidInput, "sqlite lens: source "+quoteText(s.Name)+" listed twice")
		}
		seen[s.Name] = true
	}
	return nil
}

// AttachLimit raises SQLITE_LIMIT_ATTACHED on conn as far as the driver
// allows and returns the limit now in force.
func AttachLimit(conn *sql.Conn) (int, error) {
	if _, err := modernc.Limit(conn, sqlite3.SQLITE_LIMIT_ATTACHED, wantAttached); err != nil {
		return 0, dbErr("raise attach limit", err)
	}
	got, err := modernc.Limit(conn, sqlite3.SQLITE_LIMIT_ATTACHED, -1)
	if err != nil {
		return 0, dbErr("read attach limit", err)
	}
	return got, nil
}

// setup attaches the files, builds the views and locks the connection.
func (l *Lens) setup(ctx context.Context) error {
	limit, err := AttachLimit(l.conn)
	if err != nil {
		return err
	}
	if limit < len(l.sources) {
		return errs.New(errs.Unsupported, "sqlite lens: attach limit is "+strconv.Itoa(limit))
	}
	tables := map[string][]srcTable{}
	for i, s := range l.sources {
		abs, err := filepath.Abs(s.Path)
		if err != nil {
			return errs.Wrap(errs.BadPath, "sqlite lens: bad path", err)
		}
		uri := fileuri.URL(abs, "mode=ro")
		ss, err := sqlForSource(i)
		if err != nil {
			return err
		}
		l.sql = append(l.sql, ss)
		if _, err = l.conn.ExecContext(ctx, ss.attach, uri); err != nil {
			return errs.Wrap(errs.DBNotFound, "sqlite lens: attach "+quoteText(s.Name), err)
		}
		names, err := l.publicTables(ctx, i)
		if err != nil {
			return errs.Wrap(errs.CodeOf(err), "sqlite lens: source "+quoteText(s.Name), err)
		}
		for _, n := range names {
			cols, err := l.columns(ctx, i, n)
			if err != nil {
				return err
			}
			tables[n] = append(tables[n], srcTable{src: i, cols: cols})
		}
	}
	var m mask
	if l.masked {
		var maskErr error
		if m, maskErr = l.sensitive(ctx, tables); maskErr != nil {
			return maskErr
		}
		l.hidden = l.hiddenNames()
	}
	names := make([]string, 0, len(tables))
	for n := range tables {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if err := l.createViews(ctx, n, tables[n], m); err != nil {
			return err
		}
	}
	// RES 3.10: res__kinds, res__all, res__edges, res__dangling over the
	// `_all` views (resviews below).
	if err := l.createResourceViews(ctx, tables); err != nil {
		return err
	}
	// TIME 9 (timespans.go): task_time_spans + the time_spent view, built
	// from the rows before the lens locks (open spans end now).
	if err := l.createTimeSpent(ctx, tables, time.Now()); err != nil {
		return err
	}
	// TASK 6.6 (rollup.go): task_rollup over tasks_all, task_tree_all and
	// the spans above.
	if err := l.createTaskRollup(ctx, tables); err != nil {
		return err
	}
	// DELETION 3.2 (lens_trash.go): the trash view over LensOptions.Trash.
	if err := l.createTrash(ctx); err != nil {
		return err
	}
	if _, err := l.conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return dbErr("lock lens", err)
	}
	return nil
}

// srcTable is one source's copy of a table: the source index and its
// columns in table order.
type srcTable struct {
	src  int
	cols []string
}

// createViews creates "<n>_all" over every source that has table n (by
// column name, NULL where a source lacks a column) and, on a masked lens,
// the plain TEMP VIEW "<n>" over the first source that has it: TEMP shadows
// the attached files for an unqualified name, so `SELECT ip FROM
// res_server` reads the masked copy exactly where SQLite would otherwise
// have read the file (attach order).
func (l *Lens) createViews(ctx context.Context, n string, srcs []srcTable, m mask) error {
	var all []string
	for _, st := range srcs {
		for _, c := range st.cols {
			if !slices.Contains(all, c) {
				all = append(all, c)
			}
		}
	}
	tq, err := ident(n)
	if err != nil {
		return err
	}
	var parts []string
	for _, st := range srcs {
		src := literal(l.sources[st.src].Name)
		if l.sources[st.src].ownSource() {
			src = "NULL"
			if slices.Contains(st.cols, colSource) {
				src = `"` + colSource + `"`
			}
		}
		sel, selErr := m.columns(n, all, st.cols)
		if selErr != nil {
			return selErr
		}
		parts = append(parts, "SELECT "+src+` AS "source", `+sel+` FROM "`+attachedName(st.src)+`".`+tq)
	}
	vq, err := ident(n + lensViewSuffix)
	if err != nil {
		return err
	}
	if _, execErr := l.conn.ExecContext(ctx, "CREATE TEMP VIEW "+vq+" AS "+strings.Join(parts, " UNION ALL ")); execErr != nil {
		return dbErr("create view "+n+lensViewSuffix, execErr)
	}
	l.views = append(l.views, n+lensViewSuffix)
	first := srcs[0]
	if l.sources[first.src].Deleted {
		return nil // only trashed rows hold n: no nearest live copy to shadow
	}
	// FA-META-2: the deleted-rows file's copy of n, filtered to the nearest
	// store's rows, joins the plain view.
	var del *srcTable
	for i := range srcs {
		if l.sources[srcs[i].src].Deleted && slices.Contains(srcs[i].cols, colSource) {
			del = &srcs[i]
		}
	}
	if !l.masked && del == nil {
		return nil
	}
	plainCols := first.cols
	if del != nil {
		plainCols = slices.Clone(first.cols)
		for _, c := range del.cols {
			if !slices.Contains(plainCols, c) {
				plainCols = append(plainCols, c)
			}
		}
	}
	sel, err := m.columns(n, plainCols, first.cols)
	if err != nil {
		return err
	}
	body := "SELECT " + sel + ` FROM "` + attachedName(first.src) + `".` + tq
	if del != nil {
		dsel, dErr := m.columns(n, plainCols, del.cols)
		if dErr != nil {
			return dErr
		}
		body += " UNION ALL SELECT " + dsel + ` FROM "` + attachedName(del.src) + `".` + tq +
			` WHERE "` + colSource + `" = ` + literal(l.sources[first.src].Name)
	}
	if _, err := l.conn.ExecContext(ctx, "CREATE TEMP VIEW "+tq+" AS "+body); err != nil {
		return dbErr("create view "+n, err)
	}
	return nil
}

// lensViewSuffix names a lens view: "memories" → "memories_all".
const lensViewSuffix = "_all"

// publicTables lists the tables and views of source i a lens exposes: the
// typed tables, join tables and kind views recorded in its `_tables`, plus
// row_refs and row_expanded. Internal `_` tables are reachable as
// "s_<i>"._rows for debugging but get no view.
func (l *Lens) publicTables(ctx context.Context, i int) ([]string, error) {
	rs, err := l.conn.QueryContext(ctx, l.sql[i].objects)
	if err != nil {
		return nil, errs.Wrap(errs.NotAStore, "not a lore cache file", err)
	}
	defer func() { _ = rs.Close() }()
	out := append([]string{tblRefs, tblExpanded, ViewRowMeta, tblTaskTree, ViewTeamVersions}, ProvenanceViews()...) // PROVENANCE 9.2: the provenance views are readable in lore sql; METADATA 3.16: row_meta; TEAMV-VIEW: team_versions
	for rs.Next() {
		var objs string
		if err := rs.Scan(&objs); err != nil {
			return nil, dbErr("read table records", err)
		}
		rec, err := decodeObjects(objs)
		if err != nil {
			return nil, err
		}
		for _, o := range rec {
			switch o.Type {
			case objTable, objView, objJoin:
				out = append(out, o.Name)
			case objFTS:
			}
		}
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("read table records", err)
	}
	return out, nil
}

// columns lists the columns of table in source i.
func (l *Lens) columns(ctx context.Context, i int, table string) ([]string, error) {
	rs, err := l.conn.QueryContext(ctx, "SELECT name FROM pragma_table_info(?, ?) ORDER BY cid", table, attachedName(i))
	if err != nil {
		return nil, dbErr("read columns of "+table, err)
	}
	defer func() { _ = rs.Close() }()
	var out []string
	for rs.Next() {
		var c string
		if err := rs.Scan(&c); err != nil {
			return nil, dbErr("read columns of "+table, err)
		}
		out = append(out, c)
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("read columns of "+table, err)
	}
	return out, nil
}

// Views returns the lens views, sorted.
func (l *Lens) Views() []string { return slices.Clone(l.views) }

// ResolveID finds the one source holding a live row with id. Two sources
// is E_AMBIGUOUS_ID (the caller must name the store), none is E_NOT_FOUND.
func (l *Lens) ResolveID(ctx context.Context, id ids.AnyID) (LensHit, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return LensHit{}, errs.New(errs.Internal, "sqlite lens: closed")
	}
	var hits []LensHit
	for i, s := range l.sources {
		if s.Deleted {
			continue // trashed rows are no live hit (FA-META-2)
		}
		found, err := resolveIn(l.conn.QueryContext(ctx, l.sql[i].resolve, id.String()))
		if err != nil {
			return LensHit{}, err
		}
		for _, h := range found {
			if !s.Combined {
				h.Source = s.Name
			}
			if !slices.Contains(hits, h) {
				hits = append(hits, h)
			}
			if !s.Combined {
				break // one hit per attached file: its rows share one source name
			}
		}
	}
	return oneHit(id, hits)
}

// resolveIn reads the (source, tbl) rows a resolve statement returned
// (rs is closed here).
func resolveIn(rs *sql.Rows, err error) ([]LensHit, error) {
	if err != nil {
		return nil, dbErr("resolve id", err)
	}
	defer func() { _ = rs.Close() }()
	var out []LensHit
	for rs.Next() {
		var src, tbl string
		if err := rs.Scan(&src, &tbl); err != nil {
			return nil, dbErr("resolve id", err)
		}
		out = append(out, LensHit{Source: src, Table: row.Table(tbl)})
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("resolve id", err)
	}
	return out, nil
}

// oneHit turns the places an id was found into the answer: none is
// E_NOT_FOUND, two or more sources E_AMBIGUOUS_ID (the caller must name
// the store).
func oneHit(id ids.AnyID, hits []LensHit) (LensHit, error) {
	switch len(hits) {
	case 0:
		return LensHit{}, errs.New(errs.NotFound, "sqlite lens: no row "+id.String())
	case 1:
		return hits[0], nil
	}
	names := make([]string, len(hits))
	for i, h := range hits {
		names[i] = h.Source
	}
	return LensHit{}, errs.New(errs.AmbiguousID, "sqlite lens: "+id.String()+" is in "+strings.Join(names, ", ")).
		WithHint("name the store")
}

// Query runs one read-only statement and returns typed rows, with no
// time limit (QueryTimeout with 0).
func (l *Lens) Query(ctx context.Context, query string) (Result, error) {
	return l.QueryTimeout(ctx, query, 0)
}

// QueryTimeout runs one read-only statement and returns typed rows. A
// statement that is not a single SELECT / WITH / VALUES, or that names
// something a masked lens hides, is refused before it runs (E_SQL_REFUSED,
// META 3.16 / R7), and query_only refuses any write a WITH could hide (also
// E_SQL_REFUSED). timeout > 0 interrupts the statement past it
// (E_SQL_TIMEOUT, `lore sql --timeout`).
func (l *Lens) QueryTimeout(ctx context.Context, query string, timeout time.Duration) (Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Result{}, errs.New(errs.Internal, "sqlite lens: closed")
	}
	if err := checkReadOnly(query); err != nil {
		return Result{}, refused(err)
	}
	if l.masked {
		if err := checkHidden(query, l.hidden); err != nil {
			return Result{}, refused(err)
		}
	}
	qctx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		qctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	timedOut := func(err error) error {
		if timeout > 0 && ctx.Err() == nil && errors.Is(qctx.Err(), context.DeadlineExceeded) {
			return errs.Wrap(errs.SQLTimeout, "sqlite lens: the query ran longer than "+timeout.String()+" (--timeout)", err).
				WithHint("narrow the query or raise --timeout")
		}
		return queryErr(err)
	}
	defer useExpander(l.expand)()
	rs, err := l.conn.QueryContext(qctx, query)
	if err != nil {
		return Result{}, timedOut(err)
	}
	defer func() { _ = rs.Close() }()
	res, err := scanAll(rs)
	if err != nil {
		return Result{}, timedOut(err)
	}
	return res, nil
}

// refused turns a pre-flight refusal (checkReadOnly, checkHidden) into
// E_SQL_REFUSED, keeping its message (CLI_REFERENCE 5: one code for every
// statement lore sql will not run).
func refused(err error) error {
	msg := err.Error()
	var e *errs.Error
	if errors.As(err, &e) {
		msg = e.Message
	}
	return errs.New(errs.SQLRefused, strings.TrimPrefix(msg, "sqlite lens: ")).
		WithHint("lore sql is read-only; change data with lore commands so it reaches git")
}

// queryErr maps a driver error of a lens query: SQLITE_READONLY (a write
// query_only or mode=ro stopped) is E_READ_ONLY, anything else is the
// caller's SQL (E_INVALID_INPUT). An error that already carries a code
// keeps it.
func queryErr(err error) error {
	if errs.CodeOf(err) != errs.Internal {
		return err
	}
	var se *modernc.Error
	if errors.As(err, &se) && se.Code()&primaryCodeMask == sqlite3.SQLITE_READONLY {
		return errs.Wrap(errs.SQLRefused, "lore sql is read-only; change data with lore commands so it reaches git", err)
	}
	// A compile error of the caller's own SQL (SQLITE_ERROR: "no such
	// column: ipp", a syntax error) is the caller's whole answer, and
	// surfaces print only the message, so its text goes into the message.
	// It names only what the query named, never a path (spec 9.4 keeps
	// driver text out of messages otherwise): any other driver error keeps
	// the generic message with the text in Cause.
	if errors.As(err, &se) && se.Code()&primaryCodeMask == sqlite3.SQLITE_ERROR {
		return errs.Wrap(errs.InvalidInput, "sqlite lens: "+sqliteErrText(se), err)
	}
	return errs.Wrap(errs.InvalidInput, "sqlite lens: query", err)
}

// sqliteErrText is the message of a driver error without its trailing
// " (<code>)" (modernc appends the extended result code).
func sqliteErrText(se *modernc.Error) string {
	msg := se.Error()
	if i := strings.LastIndex(msg, " ("); i > 0 && strings.HasSuffix(msg, ")") {
		msg = msg[:i]
	}
	return msg
}

// primaryCodeMask keeps the primary result code of an extended one.
const primaryCodeMask = 0xff

// Close releases the connection; idempotent.
func (l *Lens) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return errors.Join(l.conn.Close(), l.db.Close())
}

// lore_expand (SECRETS_AND_REFERENCES 12.1, SQ4, SQ15):
//
//	Lens.Query ─▶ useExpander(l.expand) ─▶ SQLite calls lore_expand(text)
//	           ─▶ activeExpand.Expand(text) ─▶ text with var/res/row filled
//
// modernc.org/sqlite registers SQL functions process-wide only (no
// per-connection API), so the function is registered once at init and
// reads the expander of the lens query running now. expandMu is held for
// the whole query, so two lenses never see each other's expander; the
// variable is set only inside that window (no state outlives a query).
const loreExpandFunc = "lore_expand"

var (
	expandMu     sync.Mutex
	activeExpand Expander
)

func init() {
	modernc.MustRegisterFunction(loreExpandFunc, &modernc.FunctionImpl{NArgs: 1, Scalar: loreExpand})
}

// useExpander makes e the expander of lore_expand until the returned func
// runs.
func useExpander(e Expander) func() {
	expandMu.Lock()
	activeExpand = e
	return func() {
		activeExpand = nil
		expandMu.Unlock()
	}
}

// loreExpand is lore_expand(text): NULL stays NULL, a non-text argument is
// returned as is (driver boundary: SQLite hands any of its value types).
func loreExpand(_ *modernc.FunctionContext, args []driver.Value) (driver.Value, error) {
	var text string
	switch v := args[0].(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return v, nil
	}
	if activeExpand == nil {
		return text, nil
	}
	out, _, err := activeExpand.Expand(text)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Resource helper views (RESOURCES_DESIGN 3.10, "Names"): built once per
// lens over the `_all` views, so they span every attached source.
//
//	res__kinds     source, id, name, description, table_name, view_name, fields
//	res__all       source, id, kind, name, scope, archived (one row per resource)
//	res__edges     source, src_id, field, dst_id (one row per set ref value)
//	res__dangling  source, src_id, name, field, dst_id (an edge whose dst is in no source)
//
// scope is the source (store) name: the cache keeps no scope label per row.
// On a masked lens a sensitive ref field makes no edge (RQ1: which resource
// it points at is part of the value).
const (
	ViewResKinds    = "res__kinds"
	ViewResAll      = "res__all"
	ViewResEdges    = "res__edges"
	ViewResDangling = "res__dangling"
)

// resKindCol is the resources column holding the row's knd_ id.
const resKindCol = "kind"

// ResourceViews lists the resource helper views a lens builds.
func ResourceViews() []string {
	return []string{ViewResKinds, ViewResAll, ViewResEdges, ViewResDangling}
}

// resViewNeeds is every (table, column) the resource views read; a lens
// whose sources lack one (an old cache) gets no resource views.
var resViewNeeds = map[string][]string{
	schema.TableResources:  {"id", fieldName, resKindCol, "archived_at", "fields"},
	schema.TableKinds:      {"id", fieldName, "description"},
	schema.TableKindFields: {"id", fieldName, fieldKindID, "type", fieldSensitive},
}

// createResourceViews creates the four res__ views when every table and
// column they read is in the lens.
func (l *Lens) createResourceViews(ctx context.Context, tables map[string][]srcTable) error {
	for t, cols := range resViewNeeds {
		have := map[string]bool{}
		for _, st := range tables[t] {
			for _, c := range st.cols {
				have[c] = true
			}
		}
		for _, c := range cols {
			if !have[c] {
				return nil
			}
		}
	}
	res := `"` + schema.TableResources + lensViewSuffix + `"`
	kinds := `"` + schema.TableKinds + lensViewSuffix + `"`
	kf := `"` + schema.TableKindFields + lensViewSuffix + `"`
	sensitiveCut := ""
	if l.masked {
		sensitiveCut = ` AND COALESCE(f."` + fieldSensitive + `", 0) = 0`
	}
	views := []struct{ name, body string }{
		{ViewResKinds, `SELECT k.source, k.id, k.name, k.description, '` + schema.KindTablePrefix + `' || k.name AS table_name, '` +
			schema.KindTablePrefix + `' || k.id AS view_name, (SELECT COUNT(DISTINCT f.id) FROM ` + kf + ` f WHERE f.kind_id = k.id) AS fields FROM ` + kinds + ` k`},
		// the nearest source's kind name wins (the _all views list sources in attach order, repo first)
		{ViewResAll, `SELECT r.source, r.id, (SELECT k.name FROM ` + kinds + ` k WHERE k.id = r."` + resKindCol + `" LIMIT 1) AS kind, ` +
			`r.name, r.source AS scope, r.archived_at IS NOT NULL AS archived FROM ` + res + ` r`},
		// a ref value is stored as {"fld_…": {"name", "v": "<res id>"}} in resources.fields
		{ViewResEdges, `SELECT r.source, r.id AS src_id, f.name AS field, json_extract(r.fields, '$."' || f.id || '".v') AS dst_id FROM ` + res +
			` r JOIN (SELECT DISTINCT id, kind_id, name, type, "` + fieldSensitive + `" FROM ` + kf + `) f ON f.kind_id = r."` + resKindCol +
			`" AND f.type = '` + string(value.KindRef) + `'` + sensitiveCut + ` WHERE json_valid(r.fields) AND json_extract(r.fields, '$."' || f.id || '".v') IS NOT NULL`},
		{ViewResDangling, `SELECT e.source, e.src_id, (SELECT s.name FROM ` + res + ` s WHERE s.id = e.src_id LIMIT 1) AS name, e.field, e.dst_id FROM "` +
			ViewResEdges + `" e WHERE NOT EXISTS (SELECT 1 FROM ` + res + ` d WHERE d.id = e.dst_id)`},
	}
	for _, v := range views {
		if _, err := l.conn.ExecContext(ctx, `CREATE TEMP VIEW "`+v.name+`" AS `+v.body); err != nil {
			return dbErr("create view "+v.name, err)
		}
		l.views = append(l.views, v.name)
	}
	slices.Sort(l.views)
	return nil
}
