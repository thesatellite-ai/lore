package wire

// trash_local.go — the opstrash.LocalCopies upgrade (`lore trash list
// --local`, `lore trash restore --local <n>`; CLI_REFERENCE 3.9, DQ1):
// this machine's v0.1.17 safety copies, read from the checkout's
// .lore/lore.db `_lore_sync_trash` table through a read-only handle (the
// core never writes lore.db; v0.1.17 may still be using the file).
//
//	no .lore/lore.db, or no _lore_sync_trash table ──▶ no copies
//	else SELECT id, table_name, row_id, reason, at  newest first
//	the "this machine only" line goes to the notice sink (stderr)
//
// A purge is the one writer (OWNERQ-25e, superseding "the core never
// writes lore.db" for this case only): DropLocalCopies deletes the copies
// of the purged row ids, so a purged value survives in no local file.

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
)

// v017SyncTrashTable is v0.1.17's safety-copy table in lore.db
// (saas/pkg/aicoder/lsync/infra.go tblTrash).
const v017SyncTrashTable = "_lore_sync_trash"

// localTrashNote heads every --local answer (DELETION 501: the copies are
// marked "this machine only"), as v0.1.17's `lore sync trash` did.
const localTrashNote = "this machine only: local safety copies of rows sync removed (never shared)"

var (
	_ opstrash.LocalCopies      = (*App)(nil)
	_ opstrash.LocalCopyDropper = (*App)(nil)
)

// localTrashBusyMS is how long the purge's write handle waits for a
// v0.1.17 lore that may still hold lore.db (SQLite busy_timeout, ms).
const localTrashBusyMS = "5000"

// LocalTrash implements opstrash.LocalCopies.
func (a *App) LocalTrash(ctx context.Context) ([]opstrash.LocalCopy, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return nil, err
	}
	if a.opts.Notice != nil {
		a.opts.Notice(localTrashNote)
	}
	return readLocalTrash(ctx, filepath.Join(top, filepath.FromSlash(v017LoreDB)))
}

// v017ReadQuery is the sqlite query of a read-only open of the v0.1.17
// lore.db at path (sqlite.LegacyReadQuery): immutable when no -wal file is
// beside it (F-8: a read leaves no -shm/-wal in the work tree) and always
// under `lore fix --dry-run` (git.NoClone, B4-14).
func v017ReadQuery(ctx context.Context, path string) string {
	return sqlite.LegacyReadQuery(path, git.NoClone(ctx), "")
}

// readLocalTrash reads the safety copies of the lore.db at path; a missing
// file or table holds none.
func readLocalTrash(ctx context.Context, path string) ([]opstrash.LocalCopy, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, errs.Wrap(errs.Internal, "trash --local: stat "+v017LoreDB, err)
	}
	dsn := fileuri.URL(path, v017ReadQuery(ctx, path))
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "trash --local: open "+v017LoreDB, err)
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to flush
	var n int
	if qerr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, v017SyncTrashTable).Scan(&n); qerr != nil {
		return nil, errs.Wrap(errs.Internal, "trash --local: read "+v017LoreDB, qerr)
	}
	if n == 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id, table_name, row_id, reason, at FROM `+v017SyncTrashTable+` ORDER BY id DESC`)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "trash --local: list "+v017SyncTrashTable, err)
	}
	defer func() { _ = rows.Close() }() // a read-only cursor: a close error loses nothing
	var out []opstrash.LocalCopy
	for rows.Next() {
		var c opstrash.LocalCopy
		if serr := rows.Scan(&c.N, &c.Table, &c.RowID, &c.Reason, &c.At); serr != nil {
			return nil, errs.Wrap(errs.Internal, "trash --local: scan "+v017SyncTrashTable, serr)
		}
		out = append(out, c)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, errs.Wrap(errs.Internal, "trash --local: list "+v017SyncTrashTable, rerr)
	}
	return out, nil
}

// DropLocalCopies implements opstrash.LocalCopyDropper (OWNERQ-25e): it
// deletes the `_lore_sync_trash` copies of rowIDs from the checkout's
// v0.1.17 lore.db and returns how many went. No lore.db or no table: 0.
// The file is never created, and the handle waits for a v0.1.17 writer.
func (a *App) DropLocalCopies(ctx context.Context, rowIDs []string) (int, error) {
	if len(rowIDs) == 0 {
		return 0, nil
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		return 0, err
	}
	path := filepath.Join(top, filepath.FromSlash(v017LoreDB))
	if _, serr := os.Stat(path); errors.Is(serr, fs.ErrNotExist) {
		return 0, nil
	} else if serr != nil {
		return 0, errs.Wrap(errs.Internal, "trash purge: stat "+v017LoreDB, serr)
	}
	// mode=rw: never create the file; busy_timeout: wait for a v0.1.17 lore.
	dsn := fileuri.URL(path, "mode=rw&_pragma=busy_timeout("+localTrashBusyMS+")")
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		return 0, errs.Wrap(errs.Internal, "trash purge: open "+v017LoreDB, err)
	}
	var n int
	if qerr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, v017SyncTrashTable).Scan(&n); qerr != nil {
		return 0, errors.Join(errs.Wrap(errs.Internal, "trash purge: read "+v017LoreDB, qerr), db.Close())
	}
	if n == 0 {
		return 0, db.Close()
	}
	// One transaction, one DELETE per id: all copies go or none do.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, errors.Join(errs.Wrap(errs.Internal, "trash purge: begin on "+v017LoreDB, err), db.Close())
	}
	var gone int64
	for _, id := range rowIDs {
		res, xerr := tx.ExecContext(ctx, `DELETE FROM `+v017SyncTrashTable+` WHERE row_id = ?`, id)
		var k int64
		if xerr == nil {
			k, xerr = res.RowsAffected()
		}
		if xerr != nil {
			return 0, errors.Join(errs.Wrap(errs.Internal, "trash purge: delete from "+v017SyncTrashTable, xerr), tx.Rollback(), db.Close())
		}
		gone += k
	}
	if err = tx.Commit(); err != nil {
		return 0, errors.Join(errs.Wrap(errs.Internal, "trash purge: commit on "+v017LoreDB, err), db.Close())
	}
	if cerr := db.Close(); cerr != nil {
		return 0, errs.Wrap(errs.Internal, "trash purge: close "+v017LoreDB, cerr)
	}
	return int(gone), nil
}
