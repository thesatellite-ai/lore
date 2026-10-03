package fts5

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dbent"
	"dbent/pkg/dbent_migrate"
)

func TestQuoteTerms(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"round-trip":         `"round-trip"`,
		"round-trip a:b":     `"round-trip" "a:b"`,
		`say "hi`:            `"say" """hi"`,
		"  spaced   words  ": `"spaced" "words"`,
		"":                   "",
	} {
		if got := QuoteTerms(in); got != want {
			t.Fatalf("QuoteTerms(%q) = %q want %q", in, got, want)
		}
	}
}

func TestIsQuerySyntaxError(t *testing.T) {
	t.Parallel()
	if !isQuerySyntaxError(errors.New("SQL logic error: no such column: trip (1)")) {
		t.Fatal("column-filter misparse must count")
	}
	if !isQuerySyntaxError(errors.New(`fts5: syntax error near "."`)) {
		t.Fatal("syntax error must count")
	}
	if isQuerySyntaxError(errors.New("database is locked")) || isQuerySyntaxError(nil) {
		t.Fatal("other errors must not trigger the literal retry")
	}
}

func TestSearch_PunctuatedFreeTextAndFTSSyntax(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lore.db")
	db := dbent.InitDB(path)
	if err := dbent.ApplyPragmas(db); err != nil {
		t.Fatal(err)
	}
	if err := dbent_migrate.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	db = dbent.InitDB(path)
	t.Cleanup(func() { _ = db.Close() })
	if !Available(ctx, db) {
		t.Skip("sqlite built without fts5")
	}
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, body := range []string{"round-trip through backup", "plain roundabout text"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO memories(id, created_at, updated_at, trust_score, source_kind, kind, body, tx_at, project_id)
			VALUES (?, ?, ?, 0.5, 'manual', 'retrieved', ?, ?, 'prj_x')`,
			[]string{"mem_01a10237c9e87e4c843d8045973a65a1", "mem_01a10237c9e87e4c843d8045973a65a2"}[i], now, now, body, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"round-trip", 1},    // '-' would be a column filter: literal retry
		{"backup:", 1},       // ':' column syntax
		{"round*", 2},        // FTS5 prefix syntax still honoured
		{"plain OR trip", 2}, // boolean syntax still honoured
		{"nothing-matches", 0},
	} {
		hits, err := Search(ctx, db, "prj_x", tc.query, 10)
		if err != nil {
			t.Fatalf("%q: %v", tc.query, err)
		}
		if len(hits) != tc.want {
			t.Fatalf("%q: %d hits, want %d", tc.query, len(hits), tc.want)
		}
	}
}

// Review finding: a column filter must scope EVERY term, including the
// several phrases the literal fallback produces.
func TestSearchEntity_ColumnFilterScopesAllTerms(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lore.db")
	db := dbent.InitDB(path)
	if err := dbent.ApplyPragmas(db); err != nil {
		t.Fatal(err)
	}
	if err := dbent_migrate.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	db = dbent.InitDB(path)
	t.Cleanup(func() { _ = db.Close() })
	if !Available(ctx, db) {
		t.Skip("sqlite built without fts5")
	}
	if err := EnsureRegistrySchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO decisions(id, created_at, updated_at, trust_score, source_kind, project_id, title, body, status)
		VALUES ('dec_01a10237c9e87e4c843d8045973a65a1', ?, ?, 0.5, 'manual', 'prj_x', 'alpha', 'beta round-trip', 'accepted')`, now, now); err != nil {
		t.Fatal(err)
	}
	cfg, ok := FindConfig("decision")
	if !ok {
		t.Fatal("decision config missing")
	}
	for _, q := range []string{"alpha beta", "alpha round-trip"} {
		hits, err := SearchEntity(ctx, db, cfg, q, SearchOptions{Columns: []string{"title"}, ProjectID: "prj_x"})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(hits) != 0 {
			t.Fatalf("%q scoped to title matched a term that is only in body", q)
		}
	}
	hits, err := SearchEntity(ctx, db, cfg, "alpha", SearchOptions{Columns: []string{"title"}, ProjectID: "prj_x"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("single in-column term: %v %v", hits, err)
	}
}
