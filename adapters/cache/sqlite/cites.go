package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
)

// qHasRefs is 1 when the file has a row_refs table (a file an older
// binary made, or one being created, may not).
const qHasRefs = `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = '` + tblRefs + `'`

// qRefIDs lists the distinct ids row_refs lines reference (ix_row_refs_ref).
const qRefIDs = `SELECT DISTINCT ref_id FROM "` + tblRefs + `"`

// FileCites reports whether the cache file at path has a row_refs line
// referencing one of refIDs, read-only, without opening the file as a
// Cache (no lock, no DDL, no refresh). A missing file cites nothing.
// Why (bug hunt H-283, devco:D-1021): a write in one folder of a personal
// store opens that folder's file only; the writer asks the store's other
// files whether a note there cites a row it just wrote, and if so moves
// the refs epoch so that file fills the note in again on its next open,
// as a file built from nothing would.
func FileCites(ctx context.Context, path string, refIDs []string) (_ bool, err error) {
	if len(refIDs) == 0 {
		return false, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, errs.Wrap(errs.BadPath, "sqlite cache: cache file path", err)
	}
	if _, err := os.Lstat(abs); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	db, err := sql.Open(driverName, fileuri.URL(abs, LegacyReadQuery(abs, false, "_pragma=busy_timeout("+strconv.Itoa(busyTimeoutMS)+")")))
	if err != nil {
		return false, dbErr("open a cache file to read its references", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	var n int
	if err := db.QueryRowContext(ctx, qHasRefs).Scan(&n); err != nil {
		return false, dbErr("read a cache file's tables", err)
	}
	if n == 0 {
		return false, nil
	}
	// the file's referenced ids are few (only text holding a reference
	// has any), the ids asked for may be a whole folder's (a first build):
	// read the file's once and look each asked id up in memory
	rs, err := db.QueryContext(ctx, qRefIDs)
	if err != nil {
		return false, dbErr("read a cache file's references", err)
	}
	defer func() { err = errors.Join(err, rs.Close()) }()
	cited := map[string]bool{}
	for rs.Next() {
		var id string
		if err := rs.Scan(&id); err != nil {
			return false, dbErr("read a cache file's references", err)
		}
		cited[id] = true
	}
	if err := rs.Err(); err != nil {
		return false, dbErr("read a cache file's references", err)
	}
	return slices.ContainsFunc(refIDs, func(id string) bool { return cited[id] }), nil
}
