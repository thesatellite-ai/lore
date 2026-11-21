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
// stops matching. Missing, unreadable or stale, it costs one full open,
// which writes a fresh one.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"

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

// regIDs keeps regID's answer per registry.
var regIDs sync.Map // *schema.Registry → string

// regID names a registry by its tables and their fields, without building
// any table plan (the executable already fixes how plans are built).
func regID(reg *schema.Registry) string {
	if v, ok := regIDs.Load(reg); ok {
		if s, ok := v.(string); ok {
			return s
		}
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
	regIDs.Store(reg, s)
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

// readStamp returns path's stamp when it is current for reg (the list on
// top of this file); ok false otherwise.
func readStamp(path string, reg *schema.Registry) (fileStamp, bool) {
	raw, err := os.ReadFile(stampPath(path))
	if err != nil {
		return fileStamp{}, false
	}
	var st fileStamp
	if json.Unmarshal(raw, &st) != nil {
		return fileStamp{}, false
	}
	exe := exeID()
	if st.Format != stampFormat || st.Layout != layoutVersion || st.DDL != ddlStamp() || exe == "" || st.Exe != exe || st.Reg != regID(reg) {
		return fileStamp{}, false
	}
	main, wal, ok := fileState(path)
	if !ok || main != st.File || wal != st.WAL {
		return fileStamp{}, false
	}
	if st.Cursors == nil {
		st.Cursors = map[string]string{}
	}
	return st, true
}

// writeStamp records c's current cursors and refs stamp beside its file.
// The values are read between two looks at the files; when a commit moved
// them in between, the stamp is removed instead (the next read opens the
// file fully). A failure only costs that full open, so none is reported.
// c.db must be usable (init done, or a commit just made).
func (c *Cache) writeStamp(ctx context.Context) {
	path := c.writes.path
	exe := exeID()
	if path == "" || exe == "" {
		return
	}
	drop := func() { _ = os.Remove(stampPath(path)) }
	main, wal, ok := fileState(path)
	if !ok {
		drop()
		return
	}
	st := fileStamp{Format: stampFormat, Layout: layoutVersion, DDL: ddlStamp(), Exe: exe, Reg: regID(c.base), File: main, WAL: wal, Cursors: map[string]string{}}
	rs, err := c.db.QueryContext(ctx, `SELECT source, cursor FROM "`+tblCursors+`"`)
	if err != nil {
		drop()
		return
	}
	for rs.Next() {
		var s, cur string
		if err := rs.Scan(&s, &cur); err != nil {
			_ = rs.Close()
			drop()
			return
		}
		st.Cursors[s] = cur
	}
	if err := errors.Join(rs.Err(), rs.Close()); err != nil {
		drop()
		return
	}
	if st.Refs, err = scanRefsStamp(c.db.QueryRowContext(ctx, qRefsStamp)); err != nil {
		drop()
		return
	}
	if main2, wal2, ok := fileState(path); !ok || main2 != main || wal2 != wal {
		drop()
		return
	}
	raw, err := json.Marshal(st)
	if err != nil {
		drop()
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+stampSuffix+".tmp-*")
	if err != nil {
		drop()
		return
	}
	_, werr := tmp.Write(raw)
	if err := errors.Join(werr, tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		drop()
		return
	}
	if err := os.Rename(tmp.Name(), stampPath(path)); err != nil {
		_ = os.Remove(tmp.Name())
		drop()
	}
}
