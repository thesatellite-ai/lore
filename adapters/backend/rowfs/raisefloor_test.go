package rowfs

import (
	"strings"
	"testing"
)

// TestRaiseFloorDoc (devco:D-735): an org floor raises a folder's
// min_lore_version, keeps its features and every other key, and never
// lowers a higher floor.
func TestRaiseFloorDoc(t *testing.T) {
	doc := []byte(`{"features":["hlc","meta"],"id":"prj_1","min_lore_version":"0.1.18","name":"app"}` + "\n")
	out, changed, err := RaiseFloorDoc(doc, "app/_meta.json", "0.1.20")
	if err != nil || !changed {
		t.Fatalf("raise: changed=%v err=%v", changed, err)
	}
	for _, want := range []string{`"min_lore_version": "0.1.20"`, `"hlc"`, `"meta"`, `"id": "prj_1"`, `"name": "app"`} {
		if !strings.Contains(strings.ReplaceAll(string(out), `":"`, `": "`), want) {
			t.Errorf("raised doc lacks %s:\n%s", want, out)
		}
	}
	if f, err := FloorOfDoc(out, "app/_meta.json"); err != nil || f != "0.1.20" {
		t.Errorf("FloorOfDoc = %q, %v", f, err)
	}
	if _, changed, err := RaiseFloorDoc(out, "app/_meta.json", "0.1.19"); err != nil || changed {
		t.Errorf("a lower floor changed the doc (changed=%v err=%v)", changed, err)
	}
	if _, changed, _ := RaiseFloorDoc(out, "app/_meta.json", "0.1.20"); changed {
		t.Error("the same floor changed the doc")
	}
	none := []byte(`{"id":"prj_2"}` + "\n")
	if out, changed, err := RaiseFloorDoc(none, "b/_meta.json", "0.1.20"); err != nil || !changed || !strings.Contains(string(out), "0.1.20") {
		t.Errorf("a folder without a floor: changed=%v err=%v out=%s", changed, err, out)
	}
	if _, _, err := RaiseFloorDoc([]byte("<<<<<<< ours\n"), "c/_meta.json", "0.1.20"); err == nil {
		t.Error("a conflict-marked _meta.json was raised")
	}
}
