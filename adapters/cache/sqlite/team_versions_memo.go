package sqlite

// team_versions_memo.go — the team versions answer kept beside the rows it
// was read from (D-739, issue #7). teamVersionsSQL parses the JSON of every
// row in `_rows` (every save since 0.1.18 carries a `_w` stamp, so the instr
// prefilter skips nothing): about 0.25 s of CPU on a 2,400-task project, in
// every lore doctor and lore fix run, although the rows rarely changed.
//
//	_rows ──AFTER INSERT/UPDATE/DELETE trigger──▶ _rows_gen.n += 1
//	TeamVersions: memo.gen == _rows_gen.n ─▶ the stored answer
//	              otherwise               ─▶ teamVersionsSQL, then store it
//	              under the generation read before the query
//
// Why triggers and not a bump in Apply: the triggers live in the file, so
// any writer of `_rows` (an older lore binary on the same cache file, a
// rebuild, a future write path) moves the generation; a memo can never
// outlive the rows it was computed from. The generation is read before the
// query, so a write racing the read labels the stored answer with the older
// generation, which no later read matches (the counter only grows).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const (
	tblRowsGen         = "_rows_gen"
	tblTeamVersionMemo = "_team_versions_memo"

	// rowsGenBump moves the generation; an upsert, so the first change of
	// a file without a counter row starts it.
	rowsGenBump = `INSERT INTO "` + tblRowsGen + `" (id, n) VALUES (1, 1) ON CONFLICT (id) DO UPDATE SET n = n + 1;`

	qRowsGen     = `SELECT COALESCE((SELECT n FROM "` + tblRowsGen + `" WHERE id = 1), 0)`
	qTeamMemoGet = `SELECT gen, doc FROM "` + tblTeamVersionMemo + `" WHERE id = 1`
	qTeamMemoPut = `INSERT INTO "` + tblTeamVersionMemo + `" (id, gen, doc) VALUES (1, ?, ?) ON CONFLICT (id) DO UPDATE SET gen = excluded.gen, doc = excluded.doc`
)

func init() {
	internalDDL = append(internalDDL,
		`CREATE TABLE IF NOT EXISTS "`+tblRowsGen+`" (id INTEGER PRIMARY KEY CHECK (id = 1), n INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS "`+tblTeamVersionMemo+`" (id INTEGER PRIMARY KEY CHECK (id = 1), gen INTEGER NOT NULL, doc TEXT NOT NULL)`,
		`CREATE TRIGGER IF NOT EXISTS "_rows_gen_ins" AFTER INSERT ON "`+tblRows+`" BEGIN `+rowsGenBump+` END`,
		`CREATE TRIGGER IF NOT EXISTS "_rows_gen_upd" AFTER UPDATE ON "`+tblRows+`" BEGIN `+rowsGenBump+` END`,
		`CREATE TRIGGER IF NOT EXISTS "_rows_gen_del" AFTER DELETE ON "`+tblRows+`" BEGIN `+rowsGenBump+` END`,
	)
}

// teamVersionsMemoized is QueryTeamVersions served from the memo when no
// row changed since it was stored. A memo that cannot be read (a file from
// before the memo tables, a hand edit) is a miss; a memo that cannot be
// stored (a read-only file, a busy writer) only costs the next read its
// query again, so neither is an error.
func teamVersionsMemoized(ctx context.Context, db *sql.DB) ([]TeamVersion, error) {
	var gen int64
	if err := db.QueryRowContext(ctx, qRowsGen).Scan(&gen); err != nil {
		return QueryTeamVersions(ctx, db) // no counter table yet: the plain read
	}
	var memoGen int64
	var doc string
	err := db.QueryRowContext(ctx, qTeamMemoGet).Scan(&memoGen, &doc)
	if err == nil && memoGen == gen {
		var out []TeamVersion
		if json.Unmarshal([]byte(doc), &out) == nil {
			return out, nil
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !isMissingTable(err) {
		return nil, dbErr("read team versions memo", err)
	}
	out, err := QueryTeamVersions(ctx, db)
	if err != nil {
		return nil, err
	}
	if b, merr := json.Marshal(out); merr == nil {
		_, _ = db.ExecContext(ctx, qTeamMemoPut, gen, string(b)) // best effort, see above
	}
	return out, nil
}
