package wire

// lens_deleted_test.go — later 23 (FA-META-2, DELETION 2.9): `lore sql
// --include-deleted` adds the rows in the trash of the attached stores to
// each table, its <table>_all view and time_spent; a masked lens masks a
// deleted row's sensitive field like a live one's; the deleted-rows file
// is one more lens source, so repo + 9 store caches + deleted is
// E_UNSUPPORTED (SQLite's attach limit) while the same lens without it
// answers.

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

func TestSQLIncludeDeleted(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	t.Setenv("LORE_HUMAN", "1")
	a := lensRepo(t, func(context.Context) (bool, error) { return false, nil })
	for _, s := range []string{"repo", "agency"} {
		for _, body := range []string{"keep in " + s, "trash in " + s} {
			out, err := run(t, a, opsmemory.OpAdd, `{"body":"`+body+`","to":"`+s+`"}`, true)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(body, "trash") {
				if _, err = run(t, a, opsmemory.OpDelete, `{"id":"`+idOf(t, out)+`"}`, true); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	out, err := run(t, a, opstask.OpAdd, `{"title":"billed then trashed"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	task := regexp.MustCompile(`tsk_[0-9a-f]{32}`).FindString(out)
	if _, err = run(t, a, opstask.OpTimeLog, `{"id":"`+task+`","duration":"2h","at":"2026-11-02"}`, true); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, a, opstask.OpDelete, `{"id":"`+task+`"}`, true); err != nil {
		t.Fatal(err)
	}
	kindWorld(t, a)
	if _, err = run(t, a, opssettings.OpResDelete, `{"id":"hub_prod"}`, true); err != nil {
		t.Fatal(err)
	}

	q := func(query string, deleted bool) []string {
		t.Helper()
		res, qerr := a.SQL(background(), opssql.Request{Query: query, IncludeDeleted: deleted})
		if qerr != nil {
			t.Fatalf("%s: %v", query, qerr)
		}
		return cells(res)
	}
	cases := []struct {
		query        string
		live, withDl []string
	}{
		{`SELECT source, body FROM memories_all WHERE body LIKE '%in %' ORDER BY source, body`,
			[]string{"agency|keep in agency", "repo|keep in repo"},
			[]string{"agency|keep in agency", "agency|trash in agency", "repo|keep in repo", "repo|trash in repo"}},
		{`SELECT body FROM memories WHERE body LIKE '%in repo' ORDER BY body`,
			[]string{"keep in repo"}, []string{"keep in repo", "trash in repo"}},
		{`SELECT task_id, counted_seconds FROM time_spent`, nil, []string{task + "|7200"}},
		// trashed resources lack their kind tables (DELETION 2.9 as built):
		// res_server keeps the live row only, resources_all gains the deleted one
		{`SELECT name, admin_ip FROM res_server_all ORDER BY name`, []string{"hub_stag|"}, []string{"hub_stag|"}},
		{`SELECT name FROM resources_all ORDER BY name`, []string{"hub_stag"}, []string{"hub_prod", "hub_stag"}},
	}
	for _, c := range cases {
		if got := q(c.query, false); !slices.Equal(got, c.live) {
			t.Errorf("%s: live = %q, want %q", c.query, got, c.live)
		}
		if got := q(c.query, true); !slices.Equal(got, c.withDl) {
			t.Errorf("%s: --include-deleted = %q, want %q", c.query, got, c.withDl)
		}
	}
	// the deleted resource's fields are masked like a live one's
	got := strings.Join(q(`SELECT fields FROM resources_all WHERE name = 'hub_prod'`, true), "\n")
	if strings.Contains(got, "198.51.100.7") || !strings.Contains(got, sqlite.MaskedValue) || !strings.Contains(got, "203.0.113.10") {
		t.Fatalf("a deleted row's fields on a masked lens: %s", got)
	}
}

// TestSQLIncludeDeletedAttachLimit: repo + 9 store caches is 10 lens
// sources, the most SQLite attaches; the deleted-rows file would be the
// 11th.
func TestSQLIncludeDeletedAttachLimit(t *testing.T) {
	// Heavy on SQLite: its own process keeps it off this process's
	// allocator lock (see package isolate).
	if isolate.Run(t) {
		return
	}
	repo := gitRepo(t)
	var b strings.Builder
	b.WriteString("stores:\n")
	for i := 1; i <= MaxStoreCaches; i++ {
		fmt.Fprintf(&b, "  - name: s%d\n    url: %s\n", i, folderStoreAt(t, fmt.Sprintf("sto_01a1a5aca7f57659be7320c5f4b7%04d", 100+i), rowfs.VisibilityInternal))
	}
	writeStoresYAML(t, repo, b.String())
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if _, err := a.SQL(background(), opssql.Request{Query: `SELECT count(*) FROM memories_all`}); err != nil {
		t.Fatalf("10 sources: %v", err)
	}
	_, err := a.SQL(background(), opssql.Request{Query: `SELECT count(*) FROM memories_all`, IncludeDeleted: true})
	if !errs.Has(err, errs.Unsupported) {
		t.Fatalf("10 sources + deleted: %v, want E_UNSUPPORTED", err)
	}
}
