package sqlite

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/metaquery"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestRowMetaCond runs every search operator's SQL against a real row_meta
// view (OV39-CMP, P2-META-2, P2-META-9): numbers compare value_num, ISO
// dates compare the date text, a value that is neither is E_META_OP; ':'
// and ~= are case-insensitive equality; != is a differing leaf; ? is the
// presence row; scope prefixes filter the entry's scope label; quotes in
// values stay literals.
func TestRowMetaCond(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	impact, due, tags, note := must(ids.New[ids.MetaKey]()), must(ids.New[ids.MetaKey]()), must(ids.New[ids.MetaKey]()), must(ids.New[ids.MetaKey]())
	m1, m2, m3, m4 := newMem(), newMem(), newMem(), newMem()
	name := map[string]string{m1.String(): "m1", m2.String(): "m2", m3.String(): "m3", m4.String(): "m4"}
	f.apply(c, store.SourceRepo, "1",
		f.metaMemory(m1, "one", map[ids.ID[ids.MetaKey]]string{
			impact: `{"name":"impact","scope":"repo","v":5}`,
			due:    `{"name":"due","scope":"repo","v":"2026-03-01"}`,
			tags:   `{"name":"tags","scope":"repo","v":["Alpha","beta"]}`,
		}),
		f.metaMemory(m2, "two", map[ids.ID[ids.MetaKey]]string{
			impact: `{"name":"impact","scope":"store","v":2.5}`,
			due:    `{"name":"due","scope":"store","v":"2025-12-31T10:00:00Z"}`,
			tags:   `{"name":"tags","scope":"store","v":["gamma"]}`,
		}),
		f.metaMemory(m3, "three", map[ids.ID[ids.MetaKey]]string{
			impact: `{"name":"impact","scope":"acme-hub","v":"high"}`,
			note:   `{"name":"note","scope":"acme-hub","v":"O'Brien"}`,
		}),
		f.memory(m4, newPrj(), "four"),
	)
	run := func(q string) ([]string, error) {
		t.Helper()
		keys := map[string]bool{"impact": true, "due": true, "tags": true, "note": true}
		ts := metaquery.Parse(q, func(n string) bool { return keys[n] })
		if len(ts) != 1 {
			t.Fatalf("%q: %d terms", q, len(ts))
		}
		cond, err := RowMetaCond("x", ts[0])
		if err != nil {
			return nil, err
		}
		// RowMetaCond's output is built to be put into a query (its values
		// are SQL literals); this is the seam the test checks
		query := strings.Join([]string{`SELECT DISTINCT x.entity_id FROM row_meta x WHERE x.key_name = ? AND `, cond}, "")
		rs, qerr := c.db.QueryContext(context.Background(), query, ts[0].Name())
		if qerr != nil {
			t.Fatalf("%q: %s: %v", q, cond, qerr)
		}
		defer func() { _ = rs.Close() }()
		var out []string
		for rs.Next() {
			var id string
			if serr := rs.Scan(&id); serr != nil {
				t.Fatal(serr)
			}
			out = append(out, name[id])
		}
		slices.Sort(out)
		return out, rs.Err()
	}
	cases := []struct {
		q    string
		want []string
	}{
		{"impact>3", []string{"m1"}},
		{"impact>=2.5", []string{"m1", "m2"}},
		{"impact<5", []string{"m2"}},
		{"impact<=5", []string{"m1", "m2"}}, // "high" is skipped, never compared
		{"impact>-1", []string{"m1", "m2"}},
		{"due>2026-01-01", []string{"m1"}},
		{"due<2026-01-01", []string{"m2"}}, // datetime vs date: ISO text order
		{"due>=2025-12-31T10:00:00Z", []string{"m1", "m2"}},
		{"impact:high", []string{"m3"}},
		{"impact:HIGH", []string{"m3"}},
		{"impact~=High", []string{"m3"}},
		{"impact:5", []string{"m1"}},
		{"tags:alpha", []string{"m1"}},  // a list item, case folded
		{"tags!=gamma", []string{"m1"}}, // a differing leaf (the caller adds NOT EXISTS for "no leaf equals")
		{"tags?", []string{"m1", "m2"}},
		{"note:O'Brien", []string{"m3"}},
		{`note:"zz') OR ('a'='a"`, nil}, // a quote in the value stays a literal (no injection)
		{"repo:impact?", []string{"m1"}},
		{"org:impact?", []string{"m2"}},
		{"project:acme-hub:impact?", []string{"m3"}},
		{"project:impact?", []string{"m3"}},
		{"project:other:impact?", nil},
		{"org:impact>1", []string{"m2"}},
	}
	for _, cs := range cases {
		got, err := run(cs.q)
		if err != nil {
			t.Errorf("%q: %v", cs.q, err)
			continue
		}
		if !slices.Equal(got, cs.want) {
			t.Errorf("%q = %v, want %v", cs.q, got, cs.want)
		}
	}
	for _, q := range []string{"impact>high", "due<soon", "impact>=3x"} {
		if _, err := run(q); !errs.Has(err, errs.MetaOp) {
			t.Errorf("%q: err %v, want E_META_OP", q, err)
		}
	}
	// a text term is not a condition
	if _, err := RowMetaCond("x", metaquery.Term{Kind: metaquery.Text, Word: "w"}); !errs.Has(err, errs.MetaOp) {
		t.Errorf("text term: %v", err)
	}
	// RowMetaComparable: only ordering comparisons limit the leaves
	tm := metaquery.Parse("impact>3", func(string) bool { return true })[0]
	if got := RowMetaComparable("x", tm); !strings.Contains(got, "value_num IS NOT NULL") {
		t.Errorf("comparable %q", got)
	}
	if got := RowMetaComparable("x", metaquery.Parse("impact~=a", func(string) bool { return true })[0]); got != "" {
		t.Errorf("~= comparable %q", got)
	}
}
