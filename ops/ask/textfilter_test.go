package ask

// textfilter_test.go — devco:D-846: loadRows and loadDays read statements
// that name no word, id or day of the question (kept by the read server
// until a file changes) and pick the rows in Go. They must pick exactly
// the rows the per-question LIKE statement picked, in the same order, so
// a served answer, a one-shot answer and the answer before the change are
// one answer.

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/thesatellite-ai/lore/model/value"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// textFixture is an in-memory SQLite holding the views the text loaders
// read, as plain tables, with the rows a LIKE treats in its own way: case
// (ASCII folds, non-ASCII does not), a NULL title or text (never matches),
// % and _ in a word (literal), an archived row, an id in two folders
// (nearest first), refs to a resource, day metadata and dates in a window.
func textFixture(t *testing.T) Query {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		`CREATE TABLE memories_all (id TEXT, place TEXT, body TEXT, valid_at TEXT, valid_until TEXT, created_at TEXT, source_kind TEXT, superseded_by_id TEXT, archived_at TEXT)`,
		`CREATE TABLE incidents_all (id TEXT, place TEXT, title TEXT, body TEXT, created_at TEXT)`,
		`CREATE TABLE row_refs_all (entity_table TEXT, entity_id TEXT, ref_id TEXT)`,
		`CREATE TABLE row_meta_all (entity_table TEXT, entity_id TEXT, key_name TEXT, item_idx INTEGER, value_text TEXT)`,
		`INSERT INTO memories_all VALUES
			('mem_1', 'me/work', 'Met ALICE-NG about Lumen.', NULL, NULL, '2026-11-01T00:00:00.000000000Z', NULL, NULL, NULL),
			('mem_2', 'me/work', 'Écoute: the Lumen budget is 50% done_now', '2026-11-12T09:00:00.000000000Z', '', '2026-11-02T00:00:00.000000000Z', 'imported', 'mem_9', NULL),
			('mem_3', 'me/work', NULL, NULL, NULL, '2026-11-12T00:00:00.000000000Z', NULL, NULL, NULL),
			('mem_4', 'me/work', 'archived lumen note', NULL, NULL, '2026-11-12T00:00:00.000000000Z', NULL, NULL, '2026-11-13T00:00:00.000000000Z'),
			('mem_5', 'me/home', 'écoute 50x done-now gym', NULL, '2026-12-01T00:00:00.000000000Z', '2026-10-30T00:00:00.000000000Z', 'typed', '', NULL),
			('mem_1', 'me/home', 'the same id in a farther folder about lumen', NULL, NULL, '2026-11-01T00:00:00.000000000Z', NULL, NULL, NULL),
			('mem_6', 'me/home', 'no word of the question here', NULL, NULL, '2026-09-01T00:00:00.000000000Z', NULL, NULL, NULL)`,
		`INSERT INTO incidents_all VALUES
			('inc_1', 'me/work', NULL, 'Lumen outage', '2026-11-12T10:00:00.000000000Z'),
			('inc_2', 'me/work', 'Kestrel pick', 'why we chose it', '2026-08-01T00:00:00.000000000Z'),
			('inc_3', 'me/work', 'unrelated', 'text', '2026-07-01T00:00:00.000000000Z')`,
		`INSERT INTO row_refs_all VALUES ('memories', 'mem_6', 'res_lumen'), ('incidents', 'inc_3', 'res_other'), ('memories', 'mem_6', 'res_kestrel')`,
		`INSERT INTO row_meta_all VALUES
			('memories', 'mem_6', 'day', 0, '2026-11-12'),
			('memories', 'inc_3', 'day', 0, '2026-11-11'),
			('memories', 'inc_3', 'day', 1, '2026-11-12'),
			('memories', 'mem_5', 'day', -1, '2026-11-12'),
			('memories', 'mem_5', 'other', 0, '2026-11-12')`,
	}
	for _, tbl := range []string{"decisions", "rules", "patterns", "hotfixes"} {
		stmts = append(stmts, `CREATE TABLE `+tbl+`_all (id TEXT, place TEXT, title TEXT, body TEXT, created_at TEXT, superseded_by_id TEXT, archived_at TEXT)`)
	}
	stmts = append(stmts, `INSERT INTO decisions_all VALUES
		('dec_1', 'me/work', 'Pick Postgres for lumen', 'Because WHY not', '2026-11-12T00:00:00.000000000Z', NULL, NULL),
		('dec_2', 'me/work', NULL, 'lumen with no title', '2026-11-12T00:00:00.000000000Z', NULL, NULL),
		('dec_3', 'me/work', 'Kestrel', 'decide on kestrel', '2026-11-03T00:00:00.000000000Z', 'dec_4', NULL)`)
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	return func(ctx context.Context, q string) (opssql.Result, error) {
		rs, err := db.QueryContext(ctx, q)
		if err != nil {
			return opssql.Result{}, err
		}
		defer func() { _ = rs.Close() }()
		cols, err := rs.Columns()
		if err != nil {
			return opssql.Result{}, err
		}
		var out opssql.Result
		for rs.Next() {
			cells := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range cells {
				ptrs[i] = &cells[i]
			}
			if err := rs.Scan(ptrs...); err != nil {
				return opssql.Result{}, err
			}
			row := make([]value.Value, len(cols))
			for i, c := range cells {
				if c.Valid {
					v, _ := value.TextOf(c.String)
					row[i] = v
				}
			}
			out.Rows = append(out.Rows, row)
		}
		return out, rs.Err()
	}
}

// TestTextRowsMatchTheLikeStatement: for word, topic, resource and date
// questions (with and without a matched resource), loadRows on a lens
// that keeps answers (reading only FileOnly statements, which the read
// server keeps) picks the rows, refs and days the per-question LIKE
// statement of a one-shot read picks, in its order.
func TestTextRowsMatchTheLikeStatement(t *testing.T) {
	t.Parallel()
	run := textFixture(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	lumen := &resourceRow{ID: "res_lumen", Name: "lumen", Aliases: []string{"ÉCOUTE", "50%", "done_now"}}
	kestrel := &resourceRow{ID: "res_kestrel", Name: "Kestrel"}
	cases := []struct {
		q  string
		ms []match
	}{
		{"what did we decide for lumen and why", nil},
		{"what did we decide for lumen and why", []match{{res: lumen}}},
		{"what is the status of kestrel", []match{{res: kestrel}, {res: lumen}}},
		{"everything about alice-ng", nil},
		{"what about ÉCOUTE and 50% done_now", nil},
		{"what happened on 2026-11-12", nil},
		{"what happened on 2026-11-12 with lumen", []match{{res: lumen}}},
		{"what happened last week", nil},
		{"what happened in the week of 2026-10-26", []match{{res: kestrel}}},
		{"the", nil},
	}
	for _, c := range cases {
		q := parseQuestion(c.q, now)
		var oneShot, read []string
		want, err := loadRows(context.Background(), func(ctx context.Context, s string) (opssql.Result, error) {
			oneShot = append(oneShot, s)
			return run(ctx, s)
		}, q, c.ms, false)
		if err != nil {
			t.Fatalf("%s: like statement: %v", c.q, err)
		}
		for _, s := range oneShot {
			if FileOnly(s) {
				t.Errorf("%s: a one-shot read handed every row over:\n%s", c.q, s)
			}
		}
		if len(want) == 0 && len(c.ms) > 0 {
			t.Errorf("%s: the fixture picks no row; the case proves nothing", c.q)
		}
		got, err := loadRows(context.Background(), func(ctx context.Context, s string) (opssql.Result, error) {
			read = append(read, s)
			return run(ctx, s)
		}, q, c.ms, true)
		if err != nil {
			t.Fatalf("%s: %v", c.q, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s (%d resources): rows differ from the LIKE statement\n got %+v\nwant %+v", c.q, len(c.ms), got, want)
		}
		for _, s := range read {
			if !FileOnly(s) {
				t.Errorf("%s: read a statement the read server cannot keep:\n%s", c.q, s)
			}
		}
	}
	if !strings.Contains(textSQL, "row_refs_all") || FileOnly(textSQL+" ") {
		t.Fatal("textSQL must read the refs, and FileOnly must name the statement exactly")
	}
}
