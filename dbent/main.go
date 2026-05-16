package dbent

import (
	"context"
	"database/sql"
	"dbent/gen/ent"
	"fmt"
	"lace/db"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/fatih/color"
)

type EntDB struct {
	client *ent.Client
}

func (e EntDB) Client() *ent.Client {
	return e.client
}

func New(db *sql.DB) EntDB {
	// Create an ent.Driver from `db`. Migrated from Postgres to SQLite per
	// docs/postgres-to-sqlite-migration.md.
	drv := entsql.OpenDB(dialect.SQLite, db)

	// client := ent.NewClient(ent.Driver(drv), ent.Debug())
	client := ent.NewClient(ent.Driver(&CustomDriver{drv}))

	client.Intercept(
		ent.InterceptFunc(func(next ent.Querier) ent.Querier {
			return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
				count, err := next.Query(ctx, query)
				return count, err
			})
		}),
	)

	// ent.InterceptFunc(func(next ent.Querier) ent.Querier {
	// 	return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
	// 		// Do something before the query execution.
	// 		value, err := next.Query(ctx, query)
	// 		// Do something after the query execution.
	// 		return value, err
	// 	})
	// })

	return EntDB{
		client: client,
	}
}

type CustomDriver struct {
	*entsql.Driver
}

func (d *CustomDriver) Query(ctx context.Context, query string, args, v any) error {
	err := d.Driver.Query(ctx, query, args, v)
	// fmt.Println("Custom ERROR", err)
	FileWithLineNum()
	return err
}

// FileWithLineNum return the file name and line number of the current file
func FileWithLineNum() string {
	if os.Getenv("LORE_TRACE") != "1" {
		return ""
	}
	for i := 1; i < 15; i++ {
		_, file, line, ok := runtime.Caller(i)
		if ok && (!strings.Contains(file, "/vendor") && !strings.Contains(file, "/ent") && !strings.Contains(file, "generated.go")) {
			fmt.Println(time.Now().Format(time.RFC3339), color.GreenString(file), color.GreenString(strconv.FormatInt(int64(line), 10)))
			return file + ":" + strconv.FormatInt(int64(line), 10)
		}
	}
	return ""
}

func WithTx(ctx context.Context, client *ent.Client, fn func(tx *ent.Tx) error) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if v := recover(); v != nil {
			tx.Rollback()
			panic(v)
		}
	}()
	if err := fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil {
			err = fmt.Errorf("%w: rolling back transaction: %v", err, rerr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// rollback calls to tx.Rollback and wraps the given error
// with the rollback error if occurred.
func Rollback(tx *ent.Tx, err error) error {
	if rerr := tx.Rollback(); rerr != nil {
		err = fmt.Errorf("%w: %v", err, rerr)
	}
	return err
}

/*
https://github.com/ent/ent/issues/4012
https://discord.com/channels/885059418646003782/885060855283216434/1331379976187936890
chats, err := s.db.Chat.Query().

	WithMessages(func(q *ent.MessageQuery) {
	    q.Order(ent.Desc(message.FieldCreatedAt))
	    q.Limit(1)
	}).
	All(ctx)
*/
func LimitRows(partitionBy string, limit int, orderBy ...string) func(s *entsql.Selector) {
	return func(s *entsql.Selector) {
		d := entsql.Dialect(s.Dialect())
		s.SetDistinct(false)
		if len(orderBy) == 0 {
			orderBy = append(orderBy, "id")
		}
		with := d.With("src_query").
			As(s.Clone()).
			With("limited_query").
			As(
				d.Select("*").
					AppendSelectExprAs(
						entsql.RowNumber().PartitionBy(partitionBy).OrderBy(entsql.Desc(orderBy[0])),
						"row_number",
					).
					From(d.Table("src_query")),
			)
		t := d.Table("limited_query").As(s.TableName())
		*s = *d.Select(s.UnqualifiedColumns()...).
			From(t).
			Where(entsql.LTE(t.C("row_number"), limit)).
			Prefix(with)
	}
}

/*
If the table is empty or column value does not exists then it gives error
failed to aggregate total: sql/scan: failed scanning rows: sql: Scan error on column index 0, name "sum": converting NULL to int is unsupported

// e.g
total, err := client.Payment.Query().Aggregate(AggOrZero(ent.Sum(payment.FieldAmount))).Int(ctx)
*/
func AggOrZero(inner ent.AggregateFunc) ent.AggregateFunc {
	return func(s *entsql.Selector) string {
		// if you put this code inside your generated ent package, then you can
		// uncomment this for better error handling, copied from the generated Sum func:
		/*
			if err := checkColumn(s.TableName(), field); err != nil {
					s.AddError(&ValidationError{Name: field, err: fmt.Errorf("ent: %w", err)})
					return ""
			}
		*/
		var b entsql.Builder
		b.WriteString("COALESCE")
		b.Wrap(func(b *entsql.Builder) {
			b.WriteString(inner(s))
			b.Comma()
			b.WriteString("0")
		})
		return b.String()
	}
}

// InitDB opens a SQLite database file at dbPath. The DSN includes
// shared cache + foreign-key enforcement. Callers should apply the WAL pragma
// block (PLAN.md S1.2) after open for production durability.
//
// Migrated from PostgreSQL connection-string builder per docs/postgres-to-sqlite-migration.md.
func InitDB(dbPath string) *sql.DB {
	// Pure-Go modernc.org/sqlite driver. It rejects mattn's "_fk=1"
	// param and instead enables per-connection foreign keys via
	// "_pragma=foreign_keys(1)". ent's SQLite migrator requires FK
	// to be ON at connection time, so this must be in the DSN (not
	// only in ApplyPragmas, which runs after the pool is built).
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&cache=shared", dbPath)
	return db.New(db.WithDataSourceName(dsn), db.WithDriverName("sqlite3"))
}

// ApplyPragmas applies the canonical pragma block for production SQLite.
// Run AFTER InitDB but BEFORE the first table is created (auto_vacuum is
// permanent once tables exist).
//
// Caller policy: every lore binary calls ApplyPragmas exactly once
// in its startup path. PLAN.md Round 26 ship-gate item.
func ApplyPragmas(db *sql.DB) error {
	pragmas := []string{
		// Set BEFORE any table — applies at create time.
		"PRAGMA auto_vacuum=INCREMENTAL",
		// Concurrency / durability.
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		// Performance.
		"PRAGMA cache_size=-64000",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA mmap_size=268435456",
		// Constraints.
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("apply %q: %w", p, err)
		}
	}
	return nil
}

// QuickCheck runs PRAGMA quick_check AND verifies the schema is present.
//
// SQLite's quick_check returns "ok" on a 0-byte file (it treats the empty
// file as a brand-new empty DB), and on a truncated header. We additionally
// probe for the `projects` table — if missing, the file is not a real
// aicoder DB (corrupt, truncated, or zeroed). Without this probe, CH-6
// (truncate-to-zero) silently slips past corruption detection.
//
// Catches: R23 #44, CH-6 (truncate-to-zero), SC-3 (corrupt → repair).
func QuickCheck(db *sql.DB) error {
	row := db.QueryRow("PRAGMA quick_check")
	var result string
	if err := row.Scan(&result); err != nil {
		return fmt.Errorf("quick_check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("quick_check failed: %s", result)
	}
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='projects'`,
	).Scan(&n); err != nil {
		return fmt.Errorf("schema probe: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("schema probe: projects table missing (db is empty or corrupt)")
	}
	return nil
}
