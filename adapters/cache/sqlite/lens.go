package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	modernc "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
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
type LensSource struct {
	Name string
	Path string
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
	// combined marks a lens over one combined cache file: the `source`
	// column and ResolveID read each row's own _source instead of the
	// LensSource name.
	combined bool
	closed   bool
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
	}, nil
}

// OpenLens attaches every source read-only and creates TEMP VIEW
// "<table>_all" (leading `source` column, UNION ALL over the sources that
// have the table). Tables whose columns differ between sources (two lore
// versions, two shapes of one kind) are unioned by column name, NULL where
// a source lacks a column.
func OpenLens(ctx context.Context, sources []LensSource) (*Lens, error) {
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
	l := &Lens{db: db, conn: conn, sources: slices.Clone(sources)}
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
	db, err := sql.Open(driverName, "file::memory:")
	if err != nil {
		return nil, dbErr("open lens", err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(dbErr("open lens", err), db.Close())
	}
	l := &Lens{db: db, conn: conn, sources: []LensSource{{Name: combinedSourceName, Path: path}}, combined: true}
	if err := l.setup(ctx); err != nil {
		return nil, errors.Join(err, l.Close())
	}
	return l, nil
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
	type srcTable struct {
		src  int
		cols []string
	}
	tables := map[string][]srcTable{}
	for i, s := range l.sources {
		abs, err := filepath.Abs(s.Path)
		if err != nil {
			return errs.Wrap(errs.BadPath, "sqlite lens: bad path", err)
		}
		uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: "mode=ro"}).String()
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
	names := make([]string, 0, len(tables))
	for n := range tables {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		var all []string
		for _, st := range tables[n] {
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
		for _, st := range tables[n] {
			src := literal(l.sources[st.src].Name)
			if l.combined {
				src = "NULL"
				if slices.Contains(st.cols, colSource) {
					src = `"` + colSource + `"`
				}
			}
			sel := []string{src + ` AS "source"`}
			for _, c := range all {
				cq, colErr := ident(c)
				if colErr != nil {
					return colErr
				}
				if slices.Contains(st.cols, c) {
					sel = append(sel, cq)
				} else {
					sel = append(sel, "NULL AS "+cq)
				}
			}
			parts = append(parts, "SELECT "+strings.Join(sel, ", ")+` FROM "`+attachedName(st.src)+`".`+tq)
		}
		vq, err := ident(n + lensViewSuffix)
		if err != nil {
			return err
		}
		if _, err := l.conn.ExecContext(ctx, "CREATE TEMP VIEW "+vq+" AS "+strings.Join(parts, " UNION ALL ")); err != nil {
			return dbErr("create view "+n+lensViewSuffix, err)
		}
		l.views = append(l.views, n+lensViewSuffix)
	}
	if _, err := l.conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return dbErr("lock lens", err)
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
	out := []string{tblRefs, tblExpanded}
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
		found, err := resolveIn(l.conn.QueryContext(ctx, l.sql[i].resolve, id.String()))
		if err != nil {
			return LensHit{}, err
		}
		for _, h := range found {
			if !l.combined {
				h.Source = s.Name
			}
			if !slices.Contains(hits, h) {
				hits = append(hits, h)
			}
			if !l.combined {
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

// Query runs one read-only statement and returns typed rows. A statement
// that is not a single SELECT / WITH / VALUES is refused before it runs
// (E_READ_ONLY), and query_only refuses any write a WITH could hide.
func (l *Lens) Query(ctx context.Context, query string) (Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Result{}, errs.New(errs.Internal, "sqlite lens: closed")
	}
	if err := checkReadOnly(query); err != nil {
		return Result{}, err
	}
	rs, err := l.conn.QueryContext(ctx, query)
	if err != nil {
		return Result{}, queryErr(err)
	}
	defer func() { _ = rs.Close() }()
	res, err := scanAll(rs)
	if err != nil {
		return Result{}, queryErr(err)
	}
	return res, nil
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
		return errs.Wrap(errs.ReadOnly, "sqlite lens: the lens is read-only", err)
	}
	return errs.Wrap(errs.InvalidInput, "sqlite lens: query", err)
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
