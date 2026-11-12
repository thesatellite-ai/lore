package sqlite

// localstate_adopt.go — one machine file (plain files sweep 3.2,
// 2026-11-12): the machine-local key → JSON state used to be its own
// SQLite file (<lore home>/state/local.db) beside the machine store
// (state/machine.db). It now lives in the machine store's file as one
// more table, like the doctor cache and the audit chain; an older
// local.db is moved in once and removed.
//
//	OpenLocalState(state/machine.db) ─▶ AdoptFile(state/local.db)
//	   ─▶ ATTACH ─▶ INSERT OR IGNORE every row ─▶ DETACH ─▶ remove the file

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Statements of the adoption (constants; the file name is a bound value).
const (
	adoptAttach   = `ATTACH DATABASE ? AS old_local`
	adoptHasTable = `SELECT 1 FROM old_local.sqlite_master WHERE type = 'table' AND name = 'local_state'`
	adoptCopy     = `INSERT OR IGNORE INTO main.local_state (tbl, key, doc, updated_at) SELECT tbl, key, doc, updated_at FROM old_local.local_state`
	adoptDetach   = `DETACH DATABASE old_local`
)

// sqliteSidecars are the files SQLite keeps beside a database file.

// AdoptFile moves every row of the local-state file at old into s (a row
// s already holds wins) and removes old with its sidecar files. No file at
// old: nothing to do. A row is never lost: the file is removed only after
// the copy committed.
func (s *LocalState) AdoptFile(ctx context.Context, old string) (err error) {
	if _, serr := os.Stat(old); errors.Is(serr, fs.ErrNotExist) {
		return nil
	}
	abs, err := filepath.Abs(old)
	if err != nil {
		return errs.Wrap(errs.BadPath, "localstate: bad path "+old, err)
	}
	if err = refuseSymlinks(abs); err != nil {
		return err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return dbErr("adopt local state", err)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	if _, err = conn.ExecContext(ctx, adoptAttach, abs); err != nil {
		return errs.Wrap(errs.Internal, "localstate: read the older state file "+old+" (move it aside if it is damaged; it holds usage counts, secret-check times and unsent time entries)", err)
	}
	var one int
	switch qerr := conn.QueryRowContext(ctx, adoptHasTable).Scan(&one); {
	case qerr == nil:
		if _, err = conn.ExecContext(ctx, adoptCopy); err != nil {
			_, _ = conn.ExecContext(ctx, adoptDetach)
			return dbErr("adopt local state", err)
		}
	case !errors.Is(qerr, sql.ErrNoRows):
		_, _ = conn.ExecContext(ctx, adoptDetach)
		return dbErr("adopt local state", qerr)
	}
	if _, err = conn.ExecContext(ctx, adoptDetach); err != nil {
		return dbErr("adopt local state", err)
	}
	for _, suf := range append([]string{""}, sqliteSidecars...) {
		if rerr := os.Remove(old + suf); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return errs.Wrap(errs.Internal, "localstate: remove the older state file "+old+suf+" after moving its rows (remove it by hand)", rerr)
		}
	}
	return nil
}
