//go:build userscen

package userscen

// sync_store_dup_location_sql_test.go — `lore sql` over two stores.yaml
// entries on ONE place (`stag` = `<url>?branch=staging`, `stag2` =
// `<url> --branch staging`). Decision (2026-11-08): lists and search show
// the place's rows once under the first name, but `lore sql` keeps both
// names as sources, so a query over both sees each row exactly twice, once
// per name. Before the fix the shared cache file (which holds each row
// once per name) was read under each name, so every row came back four
// times.
//
// Matrix: the row written through each name (stag, stag2) plus a control
// store (notes) × output (--format csv, the default table).

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestSyncStores_DupLocationSQLKeepsBothNames(t *testing.T) {
	st := newSyncTeam(t, 1)
	a := st.clones[0]
	r := st.w.Remote(syncSlugStore)
	a.MustLore("store", "init", "notes", r.URL(), "--yes")
	a.MustLore("store", "init", "stag", r.URL()+"?branch=staging", "--yes")
	attachTwice(t, a, r.URL()) // store add refuses the second entry; written as an older stores.yaml holds it
	a.MustLore("memory", "add", "dup alpha via stag", "--to", "stag")
	a.MustLore("memory", "add", "dup beta via stag2", "--to", "stag2")
	a.MustLore("memory", "add", "dup gamma via notes", "--to", "notes")

	q := "SELECT source, body FROM memories_all WHERE body LIKE 'dup %' ORDER BY body, source"
	want := [][2]string{
		{"stag", "dup alpha via stag"}, {"stag2", "dup alpha via stag"},
		{"stag", "dup beta via stag2"}, {"stag2", "dup beta via stag2"},
		{"notes", "dup gamma via notes"},
	}
	t.Run("csv", func(t *testing.T) {
		out := a.MustLore("sql", "run", q, "--format", "csv").Stdout
		lines := strings.Split(strings.TrimSpace(out), "\n")
		exp := []string{"source,body"}
		for _, w := range want {
			exp = append(exp, w[0]+","+w[1])
		}
		if strings.Join(lines, "\n") != strings.Join(exp, "\n") {
			t.Fatalf("lore sql --format csv:\n%s\nwant:\n%s", out, strings.Join(exp, "\n"))
		}
	})
	t.Run("table", func(t *testing.T) {
		res := a.MustLore("sql", "run", q)
		res.Says(t, "(5 rows)")
		for _, w := range want {
			assertSQLRow(t, res, w[0], w[1])
		}
	})
}

// assertSQLRow fails unless exactly one table line holds source then body.
func assertSQLRow(t *testing.T, res th.Result, source, body string) {
	t.Helper()
	n := 0
	for _, l := range strings.Split(res.Stdout, "\n") {
		if f := strings.Fields(l); len(f) > 1 && f[0] == source && strings.Join(f[1:], " ") == body {
			n++
		}
	}
	if n != 1 {
		t.Errorf("table lines with %s | %s = %d, want 1:\n%s", source, body, n, res.Stdout)
	}
}
