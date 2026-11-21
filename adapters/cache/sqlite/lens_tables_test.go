package sqlite

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// TestLensTablesBuildsOnlyTheNamedViews (devco D-813): a lens opened with
// LensOptions.Tables creates the "<table>" and "<table>_all" views of the
// named tables alone (no other table's views, no derived view), reads the
// same rows through them as a full lens, and masks the same values.
func TestLensTablesBuildsOnlyTheNamedViews(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, srcs, _, host := sensitiveWorld(t)
	full := openMasked(t, srcs, true)
	narrow, err := OpenLensWith(ctx, srcs, LensOptions{MaskSensitive: true, Tables: []string{"resources", "memories"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = narrow.Close() })
	if got := narrow.Views(); !slices.Equal(got, []string{"memories_all", "resources_all"}) {
		t.Fatalf("narrowed lens views = %v (full lens has %d), want only memories_all and resources_all", got, len(full.Views()))
	}
	for _, q := range []string{
		`SELECT source, name, fields FROM resources_all ORDER BY name`,
		`SELECT json_extract(fields, '$."` + host.String() + `".v') FROM resources_all WHERE name = 'vault_prod'`,
		`SELECT source, body FROM memories_all ORDER BY body`,
		`SELECT body FROM memories ORDER BY body`,
	} {
		want, got := dump(must(full.Query(ctx, q))), dump(must(narrow.Query(ctx, q)))
		if got != want {
			t.Fatalf("%s:\nnarrowed %s\nfull     %s", q, got, want)
		}
	}
	if res := must(narrow.Query(ctx, `SELECT json_extract(fields, '$."`+host.String()+`".v') FROM resources_all WHERE name = 'vault_prod'`)); res.Rows[0][0].String() != MaskedValue {
		t.Fatalf("narrowed lens shows a sensitive value: %v", res.Rows)
	}
	for _, q := range []string{`SELECT * FROM decisions_all`, `SELECT * FROM res_vault_all`, `SELECT * FROM time_spent`, `SELECT * FROM res__all`} {
		if _, err := narrow.Query(ctx, q); err == nil {
			t.Fatalf("%s: a narrowed lens exposed a table it was not given", q)
		}
	}
}

// TestQueryTablesNarrowsLoreSQL (devco D-837): a lens opened for one
// statement with Tables: QueryTables(q) builds the views of the tables q
// names alone (case-insensitive, "<table>_all" counts), reads the same
// rows as a full lens, and a statement that names a derived view, a
// schema table or a pragma gets the full lens (nil).
func TestQueryTablesNarrowsLoreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, srcs, _, _ := sensitiveWorld(t)
	full := openMasked(t, srcs, true)
	for _, q := range []string{
		`SELECT source, body FROM MEMORIES_all ORDER BY body`,
		`SELECT m.body, r.name FROM memories m, "resources" r ORDER BY 1, 2`,
		`SELECT 1`,
	} {
		tables := QueryTables(q)
		if len(tables) == 0 {
			t.Fatalf("%s: QueryTables = nil, want a narrowed lens", q)
		}
		narrow, err := OpenLensWith(ctx, srcs, LensOptions{MaskSensitive: true, Query: q, Tables: tables})
		if err != nil {
			t.Fatal(err)
		}
		if got := narrow.Views(); len(got) >= len(full.Views()) || slices.Contains(got, "decisions_all") {
			_ = narrow.Close()
			t.Fatalf("%s: lens views = %v, want only the named tables' (full lens has %d)", q, got, len(full.Views()))
		}
		want, got := dump(must(full.Query(ctx, q))), dump(must(narrow.Query(ctx, q)))
		_ = narrow.Close()
		if got != want {
			t.Fatalf("%s:\nnarrowed %s\nfull     %s", q, got, want)
		}
	}
	for _, q := range []string{
		`SELECT * FROM time_spent`, `SELECT * FROM res__all`, `SELECT * FROM trash`, `SELECT * FROM task_rollup`,
		`SELECT name FROM sqlite_temp_master`, `SELECT name FROM pragma_table_info('memories')`, `PRAGMA table_list`, ``,
	} {
		if got := QueryTables(q); got != nil {
			t.Fatalf("%q: QueryTables = %v, want nil (a full lens)", q, got)
		}
	}
}

// TestDerivedNamesCoverFullLens (devco D-837): every view or table a full
// lens creates is a table's "<table>" / "<table>_all" pair or one of
// derivedNames, so QueryTables never narrows away a view a statement names.
func TestDerivedNamesCoverFullLens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, srcs, _, _ := sensitiveWorld(t)
	full := openMasked(t, srcs, true)
	res := must(full.Query(ctx, `SELECT name FROM sqlite_temp_master WHERE type IN ('view', 'table') ORDER BY name`))
	names := map[string]bool{}
	for _, r := range res.Rows {
		names[r[0].String()] = true
	}
	for n := range names {
		// "<table>_all" always exists; the plain "<table>" view only in a
		// masked lens (unmasked, the plain name reads the attached table)
		if slices.Contains(derivedNames, n) || strings.HasSuffix(n, lensViewSuffix) || names[n+lensViewSuffix] {
			continue
		}
		t.Errorf("full lens creates %q, which is no table's view pair and not in derivedNames: add it there", n)
	}
}
