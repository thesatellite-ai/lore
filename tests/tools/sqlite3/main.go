// Command sqlite3 is the part of the sqlite3 shell the shell scenarios and
// chaos scripts use: `sqlite3 DB "SQL"` runs the SQL against DB and prints
// each result row with its columns joined by "|" (NULL as empty), the way
// the sqlite3 shell's default list mode does; an error goes to stderr as
// "Error: …" with exit 1.
//
// Why it exists: the sqlite3 shell is not installed on every gate machine,
// and a scenario that skipped when it was missing proved nothing there.
// tests/lib/common.sh builds this into each script's PATH shim instead, so
// every machine with Go runs the same checks. It uses the same pure-Go
// driver (modernc.org/sqlite) lore itself opens its database with.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// The file is opened read-write but never created (mode=rw): a script that
// names a database lore no longer writes (0.1.17's .lore/lore.db) fails
// with "unable to open" instead of reading an empty new file.
//
// busyTimeoutMS matches lore's own wait on a locked database, so a probe
// that races a running lore waits instead of failing with SQLITE_BUSY.
const busyTimeoutMS = 5000

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: sqlite3 DB SQL")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(path, query string) error {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=rw&_pragma=busy_timeout(%d)", path, busyTimeoutMS))
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()
	rows, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("columns: %w", err)
	}
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		out := make([]string, len(vals))
		for i, v := range vals {
			out[i] = v.String // NULL scans as "" like the shell prints it
		}
		fmt.Println(strings.Join(out, "|"))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}
	return nil
}
