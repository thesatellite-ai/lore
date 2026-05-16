package db

import (
	"database/sql"
	"fmt"
	"os"

	sqlite "modernc.org/sqlite"
)

// modernc.org/sqlite registers itself under the driver name "sqlite".
// The rest of this codebase (and ent's SQLite dialect) opens with the
// historical name "sqlite3", so register the same pure-Go driver under
// that name too. Pure Go => CGO_ENABLED=0 => trivial cross-compile.
// FTS5 is compiled into modernc by default (no build tag needed).
func init() {
	sql.Register("sqlite3", &sqlite.Driver{})
}

type DB *sql.DB

// New opens a SQLite database with the configured DSN. Defaults to
// "file:app.db?cache=shared&_fk=1" — shared cache for better concurrent reads
// and foreign-key enforcement enabled.
//
// After opening, callers should apply pragma block (WAL, busy_timeout, etc.)
// per docs/postgres-to-sqlite-migration.md and PLAN.md Round 26 ship-gate.
//
// SQLite migration applied per docs/postgres-to-sqlite-migration.md (R-mig).
func New(opts ...Option) *sql.DB {
	cfg := config{
		DataSourceName: "file:app.db?cache=shared&_fk=1",
		DriverName:     "sqlite3",
	}

	cfg.options(opts...)

	db, err := sql.Open(cfg.DriverName, cfg.DataSourceName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}

	return db
}
