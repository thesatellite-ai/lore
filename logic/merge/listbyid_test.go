package merge

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Fixed-width timestamps (text order is time order) for list-by-id items.
const (
	tsOld = "2026-11-04T10:00:00.000Z"
	tsNew = "2026-11-04T11:00:00.000Z"
	idA   = "tme_01936f0000007a1b8c2d3e4f5a6b7c01"
	idB   = "tme_01936f0000007a1b8c2d3e4f5a6b7c02"
	idC   = "tme_01936f0000007a1b8c2d3e4f5a6b7c03"
)

// item builds one list item; extra is appended raw inside the object.
func item(id, updated, extra string) string {
	s := `{"id":"` + id + `","updated_at":"` + updated + `"`
	if extra != "" {
		s += "," + extra
	}
	return s + "}"
}

// list builds a JSON list value from items.
func list(t *testing.T, items ...string) value.Value {
	t.Helper()
	v, err := value.JSONOf([]byte("[" + strings.Join(items, ",") + "]"))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// ids of a merged list, in order, with the "seconds" member of each.
func summary(t *testing.T, v value.Value) string {
	t.Helper()
	items, err := listItems(v)
	if err != nil {
		t.Fatal(err)
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		p := it.id[len(it.id)-2:]
		if sec := memberRaw(t, it.raw, "seconds"); sec != "" {
			p += "=" + sec
		}
		if memberText(it.raw, "deleted_at") != "" {
			p += "†"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

func memberRaw(t *testing.T, obj []byte, key string) string {
	t.Helper()
	ms, err := canonMembers(obj)
	if err != nil {
		t.Fatal(err)
	}
	return ms[key]
}

// TestStrategyListByID is the table of TIME_TRACKING_DESIGN 7.2: union by
// id, the newer updated_at wins the whole item, a tombstone is an edit,
// equal stamps settle by bytes; every case is checked in both orders and
// must give the same bytes (the merge never depends on which side is ours).
func TestStrategyListByID(t *testing.T) {
	t.Parallel()
	del := `"deleted_at":"` + tsNew + `"`
	cases := []struct {
		name string
		a, b value.Value
		want string
	}{
		{"both absent", nil, nil, ""},
		{"null and absent", value.Null{}, nil, ""},
		{"empty lists", list(t), list(t), ""},
		{"one side only", list(t, item(idA, tsOld, `"seconds":5`)), nil, "01=5"},
		{"add/add", list(t, item(idB, tsOld, `"seconds":2`)), list(t, item(idA, tsOld, `"seconds":1`)), "01=1 02=2"},
		{"add/add three, sorted by id", list(t, item(idC, tsOld, ""), item(idA, tsOld, "")), list(t, item(idB, tsOld, "")), "01 02 03"},
		{"add/edit", list(t, item(idA, tsOld, `"seconds":1`)), list(t, item(idA, tsNew, `"seconds":9`), item(idB, tsOld, `"seconds":2`)), "01=9 02=2"},
		{"edit/edit newer wins", list(t, item(idA, tsNew, `"seconds":7`)), list(t, item(idA, tsOld, `"seconds":3`)), "01=7"},
		{"edit/edit same stamp larger bytes win", list(t, item(idA, tsOld, `"seconds":3`)), list(t, item(idA, tsOld, `"seconds":7`)), "01=7"},
		{"edit/delete: newer tombstone wins", list(t, item(idA, tsOld, `"seconds":3`)), list(t, item(idA, tsNew, `"seconds":3,`+del)), "01=3†"},
		{"edit/delete: newer edit wins over older tombstone", list(t, item(idA, tsNew, `"seconds":4`)), list(t, item(idA, tsOld, `"deleted_at":"`+tsOld+`"`)), "01=4"},
		{"delete/delete", list(t, item(idA, tsNew, del)), list(t, item(idA, tsNew, del)), "01†"},
		{"identical", list(t, item(idA, tsOld, `"seconds":1`)), list(t, item(idA, tsOld, `"seconds":1`)), "01=1"},
		{"unicode note survives", list(t, item(idA, tsOld, `"note":"ドキュメント ✓"`)), list(t), "01"},
	}
	for _, c := range cases {
		ab, err := StrategyListByID(c.a, c.b)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		ba, err := StrategyListByID(c.b, c.a)
		if err != nil {
			t.Fatalf("%s (swapped): %v", c.name, err)
		}
		if got := summary(t, ab); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if ab.String() != ba.String() {
			t.Errorf("%s: order-dependent:\n%s\n%s", c.name, ab, ba)
		}
		// Both sides as decoded from their row files (a List of JSON
		// items, not value.JSON) give the same bytes.
		fa, fb := fileForm(t, c.a), fileForm(t, c.b)
		ff, err := StrategyListByID(fa, fb)
		if err != nil {
			t.Fatalf("%s (file form): %v", c.name, err)
		}
		if ff.String() != ab.String() {
			t.Errorf("%s: file form %s, memory form %s", c.name, ff, ab)
		}
	}
}

// fileForm is v as row.Decode would hand it back (value.Infer of its
// JSON); nil stays nil.
func fileForm(t *testing.T, v value.Value) value.Value {
	t.Helper()
	raw, ok := value.JSONForm(v)
	if !ok {
		return v
	}
	out, err := value.Infer(raw)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestStrategyListByIDRefuses: a value that is not a list of objects each
// carrying a text id is refused (E_INVALID_INPUT), on either side.
func TestStrategyListByIDRefuses(t *testing.T) {
	t.Parallel()
	good := list(t, item(idA, tsOld, ""))
	bad := []string{`{"id":"x"}`, `"text"`, `42`, `[1]`, `[{"updated_at":"x"}]`, `[{"id":5}]`, `[{"id":""}]`, `[{"id":"a"},"x"]`}
	for _, raw := range bad {
		v, err := value.JSONOf([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		for _, pair := range [][2]value.Value{{v, good}, {good, v}} {
			if _, err := StrategyListByID(pair[0], pair[1]); !errs.Has(err, errs.InvalidInput) {
				t.Errorf("%s accepted: %v", raw, err)
			}
		}
	}
}

// TestRowsListByIDField: in a full row merge, a list-by-id field both sides
// changed is unioned (not newest-wins), and a corrupt list on one side falls
// back to the row rule (newer side) instead of failing the merge.
func TestRowsListByIDField(t *testing.T) {
	t.Parallel()
	def := schema.Table(schema.TableMemories, testDef.Prefix,
		schema.Text("body"),
		schema.JSON("entries", schema.MergeBy(schema.MergeListByID)),
	)
	p := PolicyFor(def)
	key := text("entries")
	base := live(stamp(0, actorA), key, list(t))
	ours := live(stamp(10, actorA), key, list(t, item(idA, tsOld, `"seconds":1`)))
	theirs := live(stamp(20, actorB), key, list(t, item(idB, tsOld, `"seconds":2`)))
	res, err := Rows(p, base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if got := summary(t, res.Row.Fields["entries"]); got != "01=1 02=2" {
		t.Errorf("union: %q", got)
	}
	if !res.Clean() {
		t.Errorf("conflicts: %v", res.Conflicts)
	}
	corrupt, err := value.JSONOf([]byte(`{"not":"a list"}`))
	if err != nil {
		t.Fatal(err)
	}
	theirs = live(stamp(20, actorB), key, corrupt)
	res, err = Rows(p, base, ours, theirs)
	if err != nil {
		t.Fatalf("corrupt list failed the merge: %v", err)
	}
	if got := res.Row.Fields["entries"].String(); got != corrupt.String() {
		t.Errorf("corrupt side: row rule should take the newer side, got %s", got)
	}
}

// canonMembers maps an object's member keys to their raw JSON.
func canonMembers(obj []byte) (map[string]string, error) {
	ms, err := canon.Members(obj)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(ms))
	for _, m := range ms {
		out[m.Key] = string(m.Raw)
	}
	return out, nil
}
