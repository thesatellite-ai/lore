package settings_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestFieldRetypeKeepsTypeHistory (RES 3.14): every retype of a kind field
// appends one {type, at} entry to the field's type_history; a second
// retype keeps the first entry (the stored list is read back from the row,
// whatever value shape the read hands over).
func TestFieldRetypeKeepsTypeHistory(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"service"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"port","type":"text"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"service","name":"api","set":["port=8080"]}`)
	h.MustYes(settings.OpFieldRetype, `{"kind":"service","field":"port","type":"number"}`)
	h.MustYes(settings.OpFieldRetype, `{"kind":"service","field":"port","type":"text"}`)
	files, err := filepath.Glob(filepath.Join(h.Repo, ".lore", "data", "kind_fields", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("kind field rows %v %v", files, err)
	}
	b, err := os.ReadFile(filepath.Clean(files[0]))
	if err != nil {
		t.Fatal(err)
	}
	var row struct {
		Type string              `json:"type"`
		Hist []map[string]string `json:"type_history"`
	}
	if err = json.Unmarshal(b, &row); err != nil {
		t.Fatal(err)
	}
	if row.Type != "text" || len(row.Hist) != 2 || row.Hist[0]["type"] != "text" || row.Hist[1]["type"] != "number" {
		t.Fatalf("type %s, type_history %v (want text, then number)", row.Type, row.Hist)
	}
	for _, e := range row.Hist {
		if e["at"] == "" {
			t.Errorf("entry %v has no at", e)
		}
	}
}
