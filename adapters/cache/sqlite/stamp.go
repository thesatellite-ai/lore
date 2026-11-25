package sqlite

// stamp.go — the open stamp of a cache file (devco:D-826).
//
// Opening a cache file used to parse its whole schema (~700 objects, ~70
// KB of SQL) and check every table against the registry, on every read,
// and the lens then parsed the same schema again when it attached the
// file: a warm `lore ask` over five folders spent ~36 ms of its ~130 ms in
// the first of those two parses alone. The stamp keeps, beside the file,
// what a read needs from that open: the source cursors and the refs
// stamp, together with what makes them true. A read whose stamp is
// current gets a handle that has not connected (sql.Open connects on
// first use); Cursor and RefreshRefs answer from the stamp, the lens is
// the only connection that parses the schema, and anything else runs the
// full init on first use (ensureInit).
//
// A stamp is current only while all of these still hold:
//   - this binary wrote it (the executable's path, size and time), so the
//     layout, internal DDL and table plans are the ones this code makes;
//   - the registry is the same (its table and field names);
//   - the file is the same file, unchanged: the main file's identity,
//     size and time and the WAL's size and time are what they were when
//     the cursors were read (read between two equal looks at the files,
//     so a commit in between leaves no stamp).
//
// Any commit by any process (an older lore included) changes the WAL or
// the main file, so a stamp never needs removing before a write: it just
// stops matching. Missing, unreadable or stale, it costs a full open.
//
// A process that committed to the file writes its stamp (devco:D-839):
// after each commit, and again after its close, because closing the last
// connection folds the WAL into the main file and so makes the stamp of the
// last commit stale. A read, a dry run included, never writes a stamp for a
// file that changed since its stamp, nor one where none is: a stale one
// costs it the full open until the next write stamps the file again.
//
// One exception (devco:D-865, public issue #95): a stamp that is stale only
// because another lore binary wrote it (an upgrade or reinstall replaced
// the executable; the file, its WAL, the layout and the registry are all
// still what the stamp names) is rewritten by the read that had to do the
// full open anyway, once that open found the file in this code's layout
// with nothing to change. Without it no read after an upgrade ever found a
// current stamp, so every read paid the full open of every folder cache
// (~40-80 ms a served ask) until each folder got a write, and folders
// nothing changes stayed slow for good. The rewrite only puts this binary's
// name on what the stamp already said about an unchanged file: the stamp
// lives in lore's cache folder, never in user data.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/schema"
)

// stampSuffix names a cache file's stamp: <file>.stamp beside it.
const stampSuffix = ".stamp"

// StampSuffix is stampSuffix for the listings that describe the files
// beside a cache (`lore where`).
const StampSuffix = stampSuffix

// stampFormat is the stamp file's own format; another value is ignored.
const stampFormat = 1

// fileStamp is the stamp file's content.
type fileStamp struct {
	Format  int               `json:"format"`
	Layout  int               `json:"layout"`
	DDL     int32             `json:"ddl"`
	Exe     string            `json:"exe"`
	Reg     string            `json:"reg"`
	File    string            `json:"file"`
	WAL     string            `json:"wal"`
	Cursors map[string]string `json:"cursors"`
	Refs    string            `json:"refs"`
}

// stampPath is where path's stamp is kept.
func stampPath(path string) string { return path + stampSuffix }

// exeID names the running executable by path, size and modification
// time; "" when it cannot be read (no stamp is then ever current).
var exeID = sync.OnceValue(func() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	fi, err := os.Stat(p)
	if err != nil {
		return ""
	}
	return p + "|" + strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
})

// lastRegID keeps regID's answer for the last registry it named: one read
// stamps every folder cache it opens (five on a usual machine) with the
// same registry. One entry, never a map per registry: each command builds
// its own registry, and a map keyed by it kept every command's registry
// for the life of the process, so the local read server grew ~150 KB a
// call (devco:D-845, the same rule as basePlans).
var lastRegID struct {
	sync.Mutex
	reg *schema.Registry
	id  string
}

// regID names a registry by its tables and their fields, without building
// any table plan (the executable already fixes how plans are built).
func regID(reg *schema.Registry) string {
	lastRegID.Lock()
	defer lastRegID.Unlock()
	if lastRegID.reg == reg && lastRegID.id != "" {
		return lastRegID.id
	}
	h := sha256.New()
	for _, def := range reg.Tables() {
		_, _ = h.Write([]byte(def.Name.String() + "\x00"))
		for _, f := range def.Fields {
			_, _ = h.Write([]byte(f.Name + "\x01" + string(f.Kind) + "\x00"))
		}
		_, _ = h.Write([]byte{'\n'})
	}
	s := hex.EncodeToString(h.Sum(nil))
	lastRegID.reg, lastRegID.id = reg, s
	return s
}

// fileState is the main file's identity, size and time, and the WAL's
// size and time ("-" when it is absent or empty: a read-only lens creates
// an empty one, and closing the last connection removes it). ok is false
// when the main file cannot be read.
func fileState(path string) (main, wal string, ok bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", "", false
	}
	main = fileIdentity(fi) + "|" + strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
	wal = "-"
	if w, err := os.Stat(path + "-wal"); err == nil && w.Size() > 0 {
		wal = strconv.FormatInt(w.Size(), 10) + "|" + strconv.FormatInt(w.ModTime().UnixNano(), 10)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", "", false
	}
	return main, wal, true
}

// stampFit is how a file's stamp stands against this process (readStamp).
type stampFit int

const (
	// stampStale: missing, unreadable, or the file, layout or registry
	// changed since it was written; a read leaves it as it is.
	stampStale stampFit = iota
	// stampOtherBinary: everything the stamp names still holds except the
	// executable (an upgrade or reinstall replaced it): the read that does
	// the full open writes it again for this binary (devco:D-865).
	stampOtherBinary
	// stampCurrent: the read opens from the stamp without connecting.
	stampCurrent
)

// readStamp returns path's stamp and how it fits this process and reg
// (the list on top of this file); the stamp is usable only when the fit
// is stampCurrent.
func readStamp(path string, reg *schema.Registry) (fileStamp, stampFit) {
	raw, err := os.ReadFile(stampPath(path))
	if err != nil {
		return fileStamp{}, stampStale
	}
	var st fileStamp
	if json.Unmarshal(raw, &st) != nil {
		return fileStamp{}, stampStale
	}
	exe := exeID()
	if st.Format != stampFormat || st.Layout != layoutVersion || st.DDL != ddlStamp() || exe == "" || st.Exe == "" || st.Reg != regID(reg) {
		return fileStamp{}, stampStale
	}
	main, wal, ok := fileState(path)
	if !ok || main != st.File || wal != st.WAL {
		return fileStamp{}, stampStale
	}
	if st.Exe != exe {
		return fileStamp{}, stampOtherBinary
	}
	if st.Cursors == nil {
		st.Cursors = map[string]string{}
	}
	return st, stampCurrent
}

// writeStamp records c's current cursors and refs stamp beside its file.
// The values are read between two looks at the files; when a commit moved
// them in between, the stamp is removed instead (the next read opens the
// file fully). A failure only costs that full open, so none is reported.
// c.db must be usable (init done, or a commit just made).
func (c *Cache) writeStamp(ctx context.Context) {
	c.stamped = stampFrom(ctx, c.db, c.writes.path, c.base)
}

// stampedFiles is what a stamp's files looked like when it was written
// (fileState); the zero value is no stamp.
type stampedFiles struct{ main, wal string }

// restamps counts restamp's runs: each one opens the file again and
// parses its whole schema. Why kept: a close that left the file as its
// last stamp saw it skips restamp (Cache.stampHolds, devco:D-848), and a
// test checks that path really skips the reopen.
var restamps atomic.Int64

// restamp stamps c's file after Close, from a connection of its own: the
// close folded this process's WAL into the main file, so the stamp written
// after the last commit no longer matches. The cursors are read again
// rather than reused, so a commit by another process in between is either
// seen or makes the two looks differ (no stamp). A file in another layout
// (an older lore wrote it since) gets no stamp. Only a handle that
// committed calls it (Cache.committed), and only when the close changed
// the file since its last stamp (stampHolds). A file removed meanwhile (the
// cache sweep, `lore cache prune`) is never opened again, so never
// re-created: its stamp went with it (devco:D-990).
func (c *Cache) restamp(ctx context.Context) {
	restamps.Add(1)
	path := c.writes.path
	if path == "" || exeID() == "" {
		return
	}
	dsn, err := fileDSNExisting(path) // a swept file stays gone (devco:D-990)
	if err != nil {
		return
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }() // read only: closing an empty WAL leaves the main file as it is
	db.SetMaxOpenConns(1)
	if !currentLayout(ctx, db, path) {
		return
	}
	stampFrom(ctx, db, path, c.base)
}

// currentLayout reports whether the file db reads is in this code's layout
// (user_version and application_id); a file in another one loses its
// stamp.
func currentLayout(ctx context.Context, db *sql.DB, path string) bool {
	var ver, app int64
	if db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver) != nil || db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&app) != nil {
		return false
	}
	if ver != layoutVersion || int32(app) != ddlStamp() {
		_ = os.Remove(stampPath(path))
		return false
	}
	return true
}

// stampHolds reports whether the stamp this handle wrote after its last
// commit still describes its file once the handle is closed: the main file
// and the WAL look exactly as they did when that stamp read the cursors.
// Why (devco:D-848): restamp opened the file again after every close of a
// handle that wrote, parsing its whole schema (~700 objects), ~22 ms of
// each served read right after a sync. A close folds the WAL into the main
// file only when it is the file's last connection; while another one keeps
// the file open (the read server's lens has every folder attached) the
// close changes nothing and the stamp is still true. Any commit by any
// process since the stamp grows the WAL or changes the main file, so a
// stale cursor never passes; a close that folded fails the check and
// restamp reads the file again.
func (c *Cache) stampHolds() bool {
	if c.stamped.main == "" {
		return false
	}
	main, wal, ok := fileState(c.writes.path)
	return ok && main == c.stamped.main && wal == c.stamped.wal
}

// stampFrom records the cursors and refs stamp db reads from the file at
// path beside it (writeStamp's work, on the handle's own connection or on
// restamp's); what the files looked like, or the zero value when it wrote
// no stamp.
func stampFrom(ctx context.Context, db *sql.DB, path string, reg *schema.Registry) stampedFiles {
	exe := exeID()
	if path == "" || exe == "" {
		return stampedFiles{}
	}
	drop := func() { _ = os.Remove(stampPath(path)) }
	main, wal, ok := fileState(path)
	if !ok {
		drop()
		return stampedFiles{}
	}
	st := fileStamp{Format: stampFormat, Layout: layoutVersion, DDL: ddlStamp(), Exe: exe, Reg: regID(reg), File: main, WAL: wal, Cursors: map[string]string{}}
	rs, err := db.QueryContext(ctx, `SELECT source, cursor FROM "`+tblCursors+`"`)
	if err != nil {
		drop()
		return stampedFiles{}
	}
	for rs.Next() {
		var s, cur string
		if err := rs.Scan(&s, &cur); err != nil {
			_ = rs.Close()
			drop()
			return stampedFiles{}
		}
		st.Cursors[s] = cur
	}
	if err := errors.Join(rs.Err(), rs.Close()); err != nil {
		drop()
		return stampedFiles{}
	}
	if st.Refs, err = scanRefsStamp(db.QueryRowContext(ctx, qRefsStamp)); err != nil {
		drop()
		return stampedFiles{}
	}
	if main2, wal2, ok := fileState(path); !ok || main2 != main || wal2 != wal {
		drop()
		return stampedFiles{}
	}
	raw, err := json.Marshal(st)
	if err != nil {
		drop()
		return stampedFiles{}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+stampSuffix+".tmp-*")
	if err != nil {
		drop()
		return stampedFiles{}
	}
	_, werr := tmp.Write(raw)
	if err := errors.Join(werr, tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		drop()
		return stampedFiles{}
	}
	if err := os.Rename(tmp.Name(), stampPath(path)); err != nil {
		_ = os.Remove(tmp.Name())
		drop()
		return stampedFiles{}
	}
	return stampedFiles{main: main, wal: wal}
}
