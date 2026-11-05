package task

import "testing"

// TestParseImportNested: an export made with --with-children and
// --with-checklist reads back parent-first, children pointing at their
// parent's index, ids-less children named <parent ext>/<n>, and the
// checklist kept with its done marks.
func TestParseImportNested(t *testing.T) {
	raw := []byte(`[
	 {"title":"A","checklist":[{"text":"one","done":true},{"text":"two","done":false}],
	  "children":[{"title":"A1","children":[{"title":"A1a"}]},{"id":"x9","title":"A2"}]},
	 {"title":"B"}]`)
	recs, err := parseImport(FromJSON, "f.json", raw, nil)
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
	if _, err := parseImport(FromJSON, "f.json", []byte(`[{"title":"A","checklist":"x"}]`), nil); err == nil {
		t.Error("a checklist that is not [{text, done}] must be refused")
	}
}
