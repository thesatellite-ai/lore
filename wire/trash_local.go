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

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
)

// v017SyncTrashTable is v0.1.17's safety-copy table in lore.db
// (saas/pkg/aicoder/lsync/infra.go tblTrash).
const v017SyncTrashTable = "_lore_sync_trash"

// localTrashNote heads every --local answer (DELETION 501: the copies are
// marked "this machine only"), as v0.1.17's `lore sync trash` did.
const localTrashNote = "this machine only: local safety copies of rows sync removed (never shared)"

var _ opstrash.LocalCopies = (*App)(nil)

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

// readLocalTrash reads the safety copies of the lore.db at path; a missing
// file or table holds none.
func readLocalTrash(ctx context.Context, path string) ([]opstrash.LocalCopy, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, errs.Wrap(errs.Internal, "trash --local: stat "+v017LoreDB, err)
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}).String()
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
