package renderhost

import (
	"fmt"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestPinnedNearDupsKeepsTheNewest is devco D-926: of the rows a render
// would pin, an import and its rewrite (source_ref), a re-entered hotfix
// (same title) and a chain of three copies each leave only the newest
// shown, whatever the store order; unrelated rows are kept.
func TestPinnedNearDupsKeepsTheNewest(t *testing.T) {
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	mk := func(table row.Table, p ids.Prefix, n, age int, f map[string]string) row.Row {
		id, err := ids.ParseAny(fmt.Sprintf("%s_019a0000000070008000%012x", p, n))
		if err != nil {
			t.Fatal(err)
		}
		fields := map[string]value.Value{}
		for k, v := range f {
			tv, terr := value.TextOf(v)
			if terr != nil {
				t.Fatal(terr)
			}
			fields[k] = tv
		}
		return row.Row{ID: id, Table: table, CreatedAt: day.AddDate(0, 0, age), Fields: fields}
	}
	r1 := mk(schema.TableRules, ids.PrefixRule, 1, 0, map[string]string{"body": "Money amounts are stored as integer cents and never as floats."})
	r2 := mk(schema.TableRules, ids.PrefixRule, 2, 30, map[string]string{"body": "Store every money amount as integer cents; no floats.", "source_ref": r1.ID.String()})
	r3 := mk(schema.TableRules, ids.PrefixRule, 3, 60, map[string]string{"body": "All money: integer cents.", "source_ref": r2.ID.String()})
	other := mk(schema.TableRules, ids.PrefixRule, 4, 10, map[string]string{"body": "Every endpoint checks the caller's role first."})
	h1 := mk(schema.TableHotfixes, ids.PrefixHotfix, 5, 0, map[string]string{"title": "Staging deploy needs a cache flush", "body": "run the flush job"})
	h2 := mk(schema.TableHotfixes, ids.PrefixHotfix, 6, 30, map[string]string{"title": "Staging deploy needs a cache flush", "body": "flush after each deploy"})
	got := pinnedNearDups([]row.Row{r3, h2, other, r1, h1, r2})
	want := map[string]string{r1.ID.String(): r3.ID.String(), r2.ID.String(): r3.ID.String(), h1.ID.String(): h2.ID.String()}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("hidden → shown:\n%v\nwant:\n%v", got, want)
	}
	if got := pinnedNearDups([]row.Row{other, r1}); got != nil {
		t.Errorf("unrelated rows paired: %v", got)
	}
	if a := hidesAttr([]string{r1.ID.String()}); a != ` hides-duplicate="`+r1.ID.String()+`"` {
		t.Errorf("hidesAttr = %q", a)
	}
}
