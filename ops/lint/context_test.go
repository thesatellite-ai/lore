package lint

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestContextFindings: each 6.12 check fires on its case and not on a
// clean pinned rule; should rules and medium hotfixes are never checked.
func TestContextFindings(t *testing.T) {
	tag := id(t, ids.PrefixTag, 9)
	tags, err := value.ListOf(value.KindText, tx(t, tag.String()))
	if err != nil {
		t.Fatal(err)
	}
	rule := func(n byte, body string, extra map[string]value.Value) sourced {
		f := map[string]value.Value{"severity": tx(t, "must"), "body": tx(t, body)}
		for k, v := range extra {
			f[k] = v
		}
		return sourced{src: store.SourceRepo, r: row.Row{ID: id(t, ids.PrefixRule, n), Fields: f}}
	}
	dupText := "money amounts are integers in cents and never floats anywhere"
	clean := map[string]value.Value{schema.FieldSummary: tx(t, "ok"), schema.FieldTagIDs: tags}
	all := map[row.Table][]sourced{
		schema.TableRules: {
			rule(1, "A clean rule.", clean),
			rule(2, "Old rule.", map[string]value.Value{"superseded_by_id": tx(t, id(t, ids.PrefixRule, 1).String()), schema.FieldSummary: tx(t, "x"), schema.FieldTagIDs: tags}),
			rule(3, "Edit services/billing/invoice.go with care.", clean),
			rule(4, strings.Repeat("long ", 400), clean),
			rule(5, dupText, clean),
			rule(6, dupText+" ok", clean),
			{src: store.SourceRepo, r: row.Row{ID: id(t, ids.PrefixRule, 7), Fields: map[string]value.Value{"severity": tx(t, "should"), "body": tx(t, "x")}}},
		},
		schema.TableHotfixes: {
			{src: store.SourceRepo, r: row.Row{ID: id(t, ids.PrefixHotfix, 8), Fields: map[string]value.Value{"severity": tx(t, "high"), "body": tx(t, "y"), "title": tx(t, "t")}}},
		},
	}
	got := codes(contextFindings(all))
	want := map[Code]int{SupersededPinned: 1, NoSummary: 0, EntryTooLong: 1, PathNoGlobs: 1, Untagged: 1, NearDuplicate: 1}
	for c, n := range want {
		if got[c] != n {
			t.Errorf("%s: got %d want %d (all %v)", c, got[c], n, got)
		}
	}
	// No summary: drop rule 1's summary.
	all[schema.TableRules][0] = rule(1, "A clean rule.", map[string]value.Value{schema.FieldTagIDs: tags})
	if n := codes(contextFindings(all))[NoSummary]; n != 1 {
		t.Errorf("no_summary: got %d want 1", n)
	}
}
