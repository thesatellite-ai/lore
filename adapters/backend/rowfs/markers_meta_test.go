package rowfs

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// withMeta rebuilds r with one meta entry per raw JSON entry, in order.
func withMeta(t *testing.T, r store.Row, entries ...string) (store.Row, []ids.ID[ids.MetaKey]) {
	t.Helper()
	full, err := row.Decode(r.Doc)
	if err != nil {
		t.Fatal(err)
	}
	full.Meta = map[ids.ID[ids.MetaKey]]value.JSON{}
	keys := make([]ids.ID[ids.MetaKey], 0, len(entries))
	for _, e := range entries {
		k, kerr := ids.New[ids.MetaKey]()
		if kerr != nil {
			t.Fatal(kerr)
		}
		j, jerr := value.JSONOf([]byte(e))
		if jerr != nil {
			t.Fatal(jerr)
		}
		full.Meta[k] = j
		keys = append(keys, k)
	}
	out, err := store.FromRow(full)
	if err != nil {
		t.Fatal(err)
	}
	return out, keys
}

// TestDecodeImportableMetaMatrix (METADATA 3.12: every conflict scanner
// recurses into meta): marker text anywhere inside a meta entry (a text
// value, a list item, an object field at depth, an allowed-value label)
// refuses the import and names the entry; a structured conflict entry
// ({"conflict": {base, ours, theirs}}) is imported with the row's other
// values; harmless entries pass. The body is clean in every case, so the
// refusal can only come from the meta scan.
func TestDecodeImportableMetaMatrix(t *testing.T) {
	g := newGen(t)
	block := "<<<<<<< 0001\nmine\n=======\ntheirs\n>>>>>>> 0002"
	q := strconv.Quote(block)
	cases := []struct {
		name    string
		entries []string
		bad     int // index of the refused entry, -1 = imported
	}{
		{"plain text value", []string{`{"name":"sprint","v":"s1"}`}, -1},
		{"number and list", []string{`{"name":"cost","v":3.5}`, `{"name":"labels","v":["a","b"]}`}, -1},
		{"marker-like line only", []string{`{"name":"note","v":"<<<<<<< not a block"}`}, -1},
		{"structured conflict entry", []string{`{"name":"cost","strict":true,"conflict":{"base":3.5,"ours":4,"theirs":5}}`, `{"name":"sprint","v":"s1"}`}, -1},
		{"structured conflict with null side", []string{`{"name":"cost","conflict":{"base":null,"ours":4,"theirs":null}}`}, -1},
		{"marker block in text value", []string{`{"name":"sprint","v":"s1"}`, `{"name":"note","v":` + q + `}`}, 1},
		{"marker block in a list item", []string{`{"name":"labels","v":["a",` + q + `]}`}, 0},
		{"marker block in a nested object field", []string{`{"name":"deploy","v":{"env":{"region":` + q + `}}}`}, 0},
		{"marker block in an allowed-value label", []string{`{"name":"stage","v":"val_x","vname":` + q + `}`}, 0},
		{"marker block inside a conflict side", []string{`{"name":"note","conflict":{"base":"a","ours":` + q + `,"theirs":"b"}}`}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := newID(t)
			r, keys := withMeta(t, g.row(id, "clean body", hlc.HLC{}), c.entries...)
			rel := RowPath(memories, id)
			got, err := decodeImportable(rel, r.Doc)
			if c.bad < 0 {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if got.ID != id {
					t.Fatalf("decoded id %v", got.ID)
				}
				return
			}
			if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "meta entry "+keys[c.bad].String()+" holds unresolved conflict markers") {
				t.Fatalf("want a refusal naming entry %d, got %v", c.bad, err)
			}
			if _, derr := DecodeRowFile(rel, r.Doc); derr != nil {
				t.Fatalf("Get's decode must still read it: %v", derr)
			}
		})
	}
}
