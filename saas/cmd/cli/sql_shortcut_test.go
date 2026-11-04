package main

import (
	"strings"
	"testing"
)

// TestSQLShortcut: `lore sql <query>` runs sql.run like `lore sql run
// <query>`, with the run verb's flags before or after the query; a bad
// flag is the run verb's usage error; no query prints the group help.
func TestSQLShortcut(t *testing.T) {
	f := newCoreFixture(t)
	f.lore("memory", "add", "--body", "hello sql")
	const q = "SELECT COUNT(*) AS n FROM memories_all"
	short := f.lore("sql", q, "--json").stdout
	long := f.lore("sql", "run", q, "--json").stdout
	if short != long || !strings.Contains(short, `"count":1`) {
		t.Fatalf("lore sql <q> must equal lore sql run <q>:\n%s\n%s", short, long)
	}
	if got := f.lore("sql", "--json", "SELECT 1 AS one").stdout; !strings.Contains(got, `"one"`) {
		t.Fatalf("a flag before the query: %s", got)
	}
	if got := f.lore("sql", "SELECT", "2", "AS", "two").stdout; !strings.Contains(got, "two") {
		t.Fatalf("several words are joined into one query: %s", got)
	}
	if r := f.run(e2eBin, "sql", "--bogus", q); r.code == 0 || !strings.Contains(r.stderr, "unknown flag: --bogus") {
		t.Fatalf("a bad flag is refused: %+v", r)
	}
	if r := f.run(e2eBin, "sql", "--show-sensitive", q); r.code != 26 || !strings.Contains(r.stderr, "E_NEEDS_CONFIRM") {
		t.Fatalf("--show-sensitive asks first, as on sql run: %+v", r)
	}
	if got := f.lore("sql").stdout; !strings.Contains(got, `lore sql "SELECT name, ip FROM res_server"`) {
		t.Fatalf("no query prints the help with the shortcut example: %s", got)
	}
}
