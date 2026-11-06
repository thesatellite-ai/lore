package task

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestParseImportNested: an export made with --with-children and
// --with-checklist reads back parent-first, children pointing at their
// parent's index, ids-less children named <parent ext>/<n>, and the
// checklist kept with its done marks.
func TestParseImportNested(t *testing.T) {
	t.Parallel()
	raw := []byte(`[
	 {"title":"A","checklist":[{"text":"one","done":true},{"text":"two","done":false}],
	  "children":[{"title":"A1","children":[{"title":"A1a"}]},{"id":"x9","title":"A2"}]},
	 {"title":"B"}]`)
	recs, _, err := parseImport(FromJSON, "f.json", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		ext    string
		parent int
	}{{"f.json:1", -1}, {"f.json:1/1", 0}, {"f.json:1/1/1", 1}, {"x9", 0}, {"f.json:2", -1}}
	if len(recs) != len(want) {
		t.Fatalf("got %d records, want %d", len(recs), len(want))
	}
	for i, w := range want {
		if recs[i].extID != w.ext || recs[i].parent != w.parent {
			t.Errorf("record %d = %s parent %d, want %s parent %d", i, recs[i].extID, recs[i].parent, w.ext, w.parent)
		}
	}
	if cl := recs[0].checklist; len(cl) != 2 || !cl[0].Done || cl[1].Done || cl[1].Text != "two" {
		t.Errorf("checklist = %+v", cl)
	}
	if _, _, err := parseImport(FromJSON, "f.json", []byte(`[{"title":"A","checklist":"x"}]`), nil); err == nil {
		t.Error("a checklist that is not [{text, done}] must be refused")
	}
}

// TestImportKeyOf: the duplicate_import key is read through the one reader
// origin.ImportOf (PROVENANCE 11.4, PROV9): src (a registered source) wins
// over the importer kind, a block without ext_id is not an import, and an
// unreadable or absent `_o` is ignored rather than failing the check.
func TestImportKeyOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, o    string
		ok         bool
		src, extID string
	}{
		{"src preferred", `{"import":{"source":"jira","src":"ext_0e45506e64357c94a87e70ef1c6bbb01","ext_id":"HUB-1"}}`, true, "ext_0e45506e64357c94a87e70ef1c6bbb01", "HUB-1"},
		{"source alone", `{"import":{"source":"csv","ext_id":"AGY-1"}}`, true, "csv", "AGY-1"},
		{"unicode ext id", `{"import":{"source":"csv","ext_id":"café/1"}}`, true, "csv", "café/1"},
		{"no ext_id", `{"import":{"source":"csv"}}`, false, "", ""},
		{"empty ext_id", `{"import":{"source":"csv","ext_id":""}}`, false, "", ""},
		{"no import block", `{"repo":"x"}`, false, "", ""},
		{"import not an object", `{"import":"csv"}`, false, "", ""},
		{"absent", ``, false, "", ""},
	}
	for _, c := range cases {
		var r row.Row
		if c.o != "" {
			j, err := value.JSONOf([]byte(c.o))
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			r.Origin = j
		}
		k, ok := importKey(r)
		if ok != c.ok || k.Source != c.src || k.ExtID != c.extID {
			t.Errorf("%s: got %+v,%v want {%s %s},%v", c.name, k, ok, c.src, c.extID, c.ok)
		}
	}
	// Two rows of one registered source group together even when one
	// names the importer kind differently (src decides, not source).
	a, _ := value.JSONOf([]byte(`{"import":{"source":"jira","src":"ext_0e45506e64357c94a87e70ef1c6bbb01","ext_id":"HUB-1"}}`))
	b, _ := value.JSONOf([]byte(`{"import":{"source":"csv","src":"ext_0e45506e64357c94a87e70ef1c6bbb01","ext_id":"HUB-1"}}`))
	ka, _ := importKey(row.Row{Origin: a})
	kb, _ := importKey(row.Row{Origin: b})
	if ka != kb {
		t.Errorf("same src, different source: %+v vs %+v", ka, kb)
	}
}
