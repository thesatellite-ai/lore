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
	"slices"
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
		`CREATE TABLE incidents_all (id TEXT, place TEXT, title TEXT, body TEXT, created_at TEXT, archived_at TEXT)`,
		// devco D-963: the notes of how the system is built answer too
		`CREATE TABLE architecture_notes_all (id TEXT, place TEXT, title TEXT, body TEXT, created_at TEXT, archived_at TEXT)`,
		`CREATE TABLE cookbook_recipes_all (id TEXT, place TEXT, title TEXT, body TEXT, created_at TEXT, archived_at TEXT)`,
		`CREATE TABLE workflows_all (id TEXT, place TEXT, name TEXT, body TEXT, created_at TEXT, archived_at TEXT)`,
		`CREATE TABLE row_refs_all (entity_table TEXT, entity_id TEXT, ref_id TEXT)`,
		`CREATE TABLE row_meta_all (entity_table TEXT, entity_id TEXT, key_name TEXT, item_idx INTEGER, value_text TEXT)`,
		// D-942: comments make their row answer; none here, so the rows
		// picked are the LIKE statement's
		`CREATE TABLE comments_all (id TEXT, entity_table TEXT, entity_id TEXT, body TEXT, created_at TEXT)`,
		`INSERT INTO memories_all VALUES
			('mem_1', 'me/work', 'Met ALICE-NG about Lumen.', NULL, NULL, '2026-11-01T00:00:00.000000000Z', NULL, NULL, NULL),
			('mem_2', 'me/work', 'Écoute: the Lumen budget is 50% done_now', '2026-11-12T09:00:00.000000000Z', '', '2026-11-02T00:00:00.000000000Z', 'imported', 'mem_9', NULL),
			('mem_3', 'me/work', NULL, NULL, NULL, '2026-11-12T00:00:00.000000000Z', NULL, NULL, NULL),
			('mem_4', 'me/work', 'archived lumen note', NULL, NULL, '2026-11-12T00:00:00.000000000Z', NULL, NULL, '2026-11-13T00:00:00.000000000Z'),
			('mem_5', 'me/home', 'écoute 50x done-now gym', NULL, '2026-12-01T00:00:00.000000000Z', '2026-10-30T00:00:00.000000000Z', 'typed', '', NULL),
			('mem_1', 'me/home', 'the same id in a farther folder about lumen', NULL, NULL, '2026-11-01T00:00:00.000000000Z', NULL, NULL, NULL),
			('mem_6', 'me/home', 'no word of the question here', NULL, NULL, '2026-09-01T00:00:00.000000000Z', NULL, NULL, NULL),
			('mem_7', 'me/life', 'Went hiking at the lake trail.', NULL, NULL, '2026-11-21T00:00:00.000000000Z', NULL, NULL, NULL),
			('mem_8', 'me/life', 'Typed up the hiking notes.', '2026-11-13T09:00:00.000000000Z', NULL, '2026-11-21T00:00:00.000000000Z', NULL, NULL, NULL)`,
		`INSERT INTO incidents_all VALUES
			('inc_1', 'me/work', NULL, 'Lumen outage', '2026-11-12T10:00:00.000000000Z', NULL),
			('inc_2', 'me/work', 'Kestrel pick', 'why we chose it', '2026-08-01T00:00:00.000000000Z', NULL),
			('inc_3', 'me/work', 'unrelated', 'text', '2026-07-01T00:00:00.000000000Z', NULL)`,
		`INSERT INTO row_refs_all VALUES ('memories', 'mem_6', 'res_lumen'), ('incidents', 'inc_3', 'res_other'), ('memories', 'mem_6', 'res_kestrel')`,
		`INSERT INTO row_meta_all VALUES
			('memories', 'mem_6', 'day', 0, '2026-11-12'),
			('memories', 'inc_3', 'day', 0, '2026-11-11'),
			('memories', 'inc_3', 'day', 1, '2026-11-12'),
			('memories', 'mem_5', 'day', -1, '2026-11-12'),
			('memories', 'mem_5', 'other', 0, '2026-11-12'),
			('memories', 'mem_7', 'day', 0, '2026-11-14'),
			('memories', 'mem_8', 'day', 0, '2026-09-01')`,
	}
	for _, tbl := range []string{"decisions", "rules", "patterns", "hotfixes"} {
		stmts = append(stmts, `CREATE TABLE `+tbl+`_all (id TEXT, place TEXT, title TEXT, body TEXT, created_at TEXT, superseded_by_id TEXT, archived_at TEXT)`)
	}
	stmts = append(stmts, `INSERT INTO decisions_all VALUES
		('dec_1', 'me/work', 'Pick Postgres for lumen', 'Because WHY not', '2026-11-12T00:00:00.000000000Z', NULL, NULL),
		('dec_2', 'me/work', NULL, 'lumen with no title', '2026-11-12T00:00:00.000000000Z', NULL, NULL),
		('dec_3', 'me/work', 'Kestrel', 'decide on kestrel', '2026-11-03T00:00:00.000000000Z', 'dec_4', NULL)`)
	// every row view has updated_at (the version ask's pickLines compares)
	for _, tbl := range []string{"memories", "incidents", "decisions", "rules", "patterns", "hotfixes", "architecture_notes", "cookbook_recipes", "workflows"} {
		stmts = append(stmts, `ALTER TABLE `+tbl+`_all ADD COLUMN updated_at TEXT`)
	}
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
// questions (with and without a matched resource), loadRows picks, from
// the kept textSQL alone (the FileOnly statements the read server keeps),
// the rows, refs and days the per-question LIKE statement (likeOracle)
// picks, in its order, and a one-shot read picks the same (devco D-902:
// both read every row, so the meaning rows and BM25 see the same corpus).
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
		{"where did I go hiking on 2026-11-13", nil},
		{"what happened on 2026-11-13", nil},
		{"the", nil},
	}
	for _, c := range cases {
		q := parseQuestion(c.q, now)
		want := likeOracle(t, run, q, c.ms)
		if len(want) == 0 && len(c.ms) > 0 {
			t.Errorf("%s: the fixture picks no row; the case proves nothing", c.q)
		}
		for _, keeps := range []bool{true, false} {
			var read []string
			got, _, _, err := loadRows(context.Background(), func(ctx context.Context, s string) (opssql.Result, error) {
				read = append(read, s)
				return run(ctx, s)
			}, q, c.ms, keeps)
			if err != nil {
				t.Fatalf("%s: %v", c.q, err)
			}
			if !reflect.DeepEqual(withoutMemos(got), withoutMemos(want)) {
				t.Errorf("%s (%d resources, keeps %v): rows differ from the LIKE statement\n got %+v\nwant %+v", c.q, len(c.ms), keeps, got, want)
			}
			for _, s := range read {
				if keeps && !FileOnly(s) {
					t.Errorf("%s: read a statement the read server cannot keep:\n%s", c.q, s)
				}
			}
		}
	}
	if !strings.Contains(textSQL, "row_refs_all") || FileOnly(textSQL+" ") {
		t.Fatal("textSQL must read the refs, and FileOnly must name the statement exactly")
	}
}

// TestDayMetadataNearTheWindow (devco D-903, bug hunt H-158): a date
// question reads the day metadata of the window widened by its fuzz and
// every day of a row written near the window, in a one-shot and a kept
// read alike. A row about 2026-11-14 (written a week later) asked about
// 2026-11-13 carries its day, so it ranks as dated near the window; a
// row written on 2026-11-13 about 2026-09-01 carries that day, so it is
// not one of the 13th's rows.
func TestDayMetadataNearTheWindow(t *testing.T) {
	t.Parallel()
	run := textFixture(t)
	for _, keeps := range []bool{false, true} {
		q := parseQuestion("where did I go hiking on 2026-11-13", now)
		rows, _, _, err := loadRows(context.Background(), run, q, nil, keeps)
		if err != nil {
			t.Fatal(err)
		}
		days := map[string]string{}
		for _, r := range rows {
			days[r.ID] = r.Day
		}
		if days["mem_7"] != "2026-11-14" || days["mem_8"] != "2026-09-01" {
			t.Errorf("keeps=%v: days %v, want mem_7 2026-11-14 (a day inside the fuzz) and mem_8 2026-09-01 (written in the window)", keeps, days)
		}
		if timeBonus(q, &rows[slices.IndexFunc(rows, func(r textRow) bool { return r.ID == "mem_7" })]) == 0 {
			t.Errorf("keeps=%v: the row about the day after the window gets no time bonus", keeps)
		}
		q = parseQuestion("what happened on 2026-11-13", now)
		if rows, _, _, err = loadRows(context.Background(), run, q, nil, keeps); err != nil {
			t.Fatal(err)
		}
		for _, r := range windowRows(q, nil, rows) {
			if r.ID == "mem_8" {
				t.Errorf("keeps=%v: a row about 2026-09-01 is listed as one of 2026-11-13's rows", keeps)
			}
		}
	}
}

// likeOracle is the rows the per-question LIKE statement picks for q (the
// one-shot read before devco D-902): what rowFilter.answers must decide
// exactly.
func likeOracle(t *testing.T, run Query, q question, ms []match) []textRow {
	t.Helper()
	ctx := context.Background()
	terms := rowTerms(q, ms)
	like := func(s string) string {
		r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
		return sqlText("%" + r.Replace(strings.ToLower(s)) + "%")
	}
	var conds, refIDs []string
	for _, w := range terms {
		conds = append(conds, "lower(%T || ' ' || %B) LIKE "+like(w)+` ESCAPE '\'`)
	}
	for _, m := range ms {
		refIDs = append(refIDs, sqlText(m.res.ID))
	}
	if len(refIDs) > 0 {
		conds = append(conds, "m.id IN (SELECT entity_id FROM row_refs_all WHERE ref_id IN ("+strings.Join(refIDs, ", ")+"))")
	}
	var days map[string][]string
	var near dayRange
	if !q.window.IsZero() {
		since, end := q.window.Since.Format(time.DateOnly), q.window.End.Format(time.DateOnly)
		if q.window.End.IsZero() {
			end = openEnd
		}
		near = nearRange(q, dayRange{since, end})
		var err error
		if days, err = loadDays(ctx, run, near, false); err != nil {
			t.Fatal(err)
		}
		dayCond := "0"
		var ids []string
		for id, items := range days {
			if slices.ContainsFunc(items, dayRange{since, end}.holds) {
				ids = append(ids, sqlText(id))
			}
		}
		if len(ids) > 0 {
			slices.Sort(ids)
			dayCond = "m.id IN (" + strings.Join(ids, ", ") + ")"
		}
		win := []string{"(%V >= " + sqlText(since) + " AND %V < " + sqlText(end) + ")", dayCond}
		if len(ms) == 0 {
			conds = append(conds, win...)
		} else {
			conds = win
		}
	}
	if len(conds) == 0 {
		return nil
	}
	if refIDs == nil {
		refIDs = []string{}
	}
	res, err := run(ctx, textStatement(strings.Join(conds, " OR "), refIDs))
	if err != nil {
		t.Fatal(err)
	}
	// in the order ask reads every line (devco:D-979: not the cache's)
	res.Rows = canonicalLines(res.Rows, textLineKey)
	refs := map[string][]string{}
	for _, row := range res.Rows {
		if cell(row[colKind]) == "ref" {
			refs[cell(row[colID])] = append(refs[cell(row[colID])], cell(row[colRefID]))
		}
	}
	var out []textRow
	seen := map[string]bool{}
	for _, row := range res.Rows {
		id := cell(row[colID])
		if cell(row[colKind]) != "row" || seen[id] {
			continue
		}
		seen[id] = true
		at, _ := time.Parse(time.RFC3339Nano, cell(row[colAt]))
		until, _ := time.Parse(time.RFC3339Nano, cell(row[colUntil]))
		r := textRow{Table: cell(row[colTable]), ID: id, Place: cell(row[colPlace]), Title: cell(row[colTitle]), Body: cell(row[colBody]), At: at, Imported: cell(row[colImported]) == "1", SupersededBy: cell(row[colSuperseded]), ValidUntil: until, Refs: refs[id], stored: cell(row[colAt])}
		if !q.window.IsZero() {
			r.Day = metaDayOf(q, days[id], r.stored, near)
		}
		out = append(out, r)
	}
	return out
}

// withoutMemos is rows without the word sets a kept corpus row shares
// (corpusOf, bug hunt H-214/H-219) and a row read once builds on use: the
// two reads answer the same rows whichever built them.
func withoutMemos(rows []textRow) []textRow {
	out := make([]textRow, len(rows))
	for i, r := range rows {
		r.words, r.first, r.content, r.label, r.labeled, r.split = nil, nil, nil, nil, false, nil
		out[i] = r
	}
	return out
}
