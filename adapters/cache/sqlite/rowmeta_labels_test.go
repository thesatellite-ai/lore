package sqlite

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// metaValueRow is a meta_values row of key with name and label ("" = none).
func (f *fixture) metaValueRow(id ids.ID[ids.MetaValue], key ids.ID[ids.MetaKey], name, label string) store.Row {
	f.t.Helper()
	fields := map[string]value.Value{"key_id": must(value.RefOf(key.Any())), fieldName: text(f.t, name)}
	if label != "" {
		fields[fieldValueLabel] = text(f.t, label)
	}
	return f.live(schema.TableMetaValues, id.Any(), fields)
}

// TestMetaAllowedValueLabelsSearch (MQ14 + R8, DECISIONS_LOG 164): a row
// whose meta names an allowed value is found by the value's current name
// and label, as a scalar or a list item, whatever order the value row and
// the row arrive in (one batch or two); a relabel re-indexes the rows; a
// stale v_name in the entry does not hide the current name; a sensitive
// key's values (labels included) are never indexed.
func TestMetaAllowedValueLabelsSearch(t *testing.T) {
	f := newFixture(t)
	c := f.open("repo.db")
	stage, secret := must(ids.New[ids.MetaKey]()), must(ids.New[ids.MetaKey]())
	urgent, later, hidden := must(ids.New[ids.MetaValue]()), must(ids.New[ids.MetaValue]()), must(ids.New[ids.MetaValue]())
	keyRow := func(id ids.ID[ids.MetaKey], name string, sensitive bool) store.Row {
		return f.live(schema.TableMetaKeys, id.Any(), map[string]value.Value{
			fieldName: text(t, name), "scope": text(t, "repo"), "type": text(t, "text"), fieldSensitive: value.BoolOf(sensitive),
		})
	}
	scalar, list, stale, early, sens := newMem(), newMem(), newMem(), newMem(), newMem()
	// batch 1: the referrer "early" arrives before its value row "later"
	f.apply(c, store.SourceRepo, "1",
		keyRow(stage, "stage", false), keyRow(secret, "secret_stage", true),
		f.metaMemory(early, "early row", map[ids.ID[ids.MetaKey]]string{stage: `{"name":"stage","v":"` + later.String() + `","v_name":"p2"}`}),
		f.metaValueRow(urgent, stage, "p1", "Urgent"),
		f.metaValueRow(later, stage, "p2", "Someday"),
		f.metaValueRow(hidden, secret, "s1", "Classified"),
		f.metaMemory(scalar, "scalar row", map[ids.ID[ids.MetaKey]]string{stage: `{"name":"stage","v":"` + urgent.String() + `","v_name":"p1"}`}),
		f.metaMemory(list, "list row", map[ids.ID[ids.MetaKey]]string{stage: `{"name":"stage","v":["` + urgent.String() + `","` + later.String() + `"],"v_name":["p1","p2"]}`}),
		f.metaMemory(stale, "stale row", map[ids.ID[ids.MetaKey]]string{stage: `{"name":"stage","v":"` + urgent.String() + `","v_name":"oldname"}`}),
		f.metaMemory(sens, "sensitive row", map[ids.ID[ids.MetaKey]]string{secret: `{"name":"secret_stage","v":"` + hidden.String() + `","v_name":"s1"}`}),
	)
	hits := func(q string) map[string]bool {
		t.Helper()
		out := map[string]bool{}
		for _, h := range must(c.Search(context.Background(), schema.TableMemories, q, 20)) {
			out[h.String()] = true
		}
		return out
	}
	expect := func(step, q string, want ...ids.AnyID) {
		t.Helper()
		got := hits(q)
		if len(got) != len(want) {
			t.Errorf("%s: search %s = %v, want %d hits", step, q, got, len(want))
			return
		}
		for _, w := range want {
			if !got[w.String()] {
				t.Errorf("%s: search %s misses %s (got %v)", step, q, w, got)
			}
		}
	}
	expect("label", "Urgent", scalar, list, stale)
	expect("name", "p1", scalar, list, stale)
	expect("list item label", "Someday", list, early)
	expect("stale v_name kept", "oldname", stale)
	expect("sensitive label", "Classified")
	expect("sensitive name", "s1")

	// a relabel re-indexes every row of the key
	f.apply(c, store.SourceRepo, "2", f.metaValueRow(urgent, stage, "p1", "Critical"))
	expect("after relabel, new", "Critical", scalar, list, stale)
	expect("after relabel, old", "Urgent")

	// a hand-edited value row whose key is not a key id re-indexes nothing
	// and never fails the apply (the id reaches a JSON path)
	bad := f.live(schema.TableMetaValues, must(ids.New[ids.MetaValue]()).Any(), map[string]value.Value{"key_id": text(t, `mky_"x`), fieldName: text(t, "p9"), fieldValueLabel: text(t, "Odd")})
	if err := c.Apply(context.Background(), store.SourceRepo, []store.Row{bad}, "3"); err != nil {
		t.Fatalf("a bad key_id fails the apply: %v", err)
	}
	expect("after a bad value row", "Critical", scalar, list, stale)
}
