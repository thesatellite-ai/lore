package task

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// TestExportWithChildren: --with-children adds every descendant once and
// the json nests them under their parent (19.2, OV47).
func TestExportWithChildren(t *testing.T) {
	mk := func(id, parent string) row.Row {
		x, err := ids.ParseAny(id)
		if err != nil {
			t.Fatal(err)
		}
		txt := func(s string) value.Text {
			v, err := value.TextOf(s)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		r := row.Row{ID: x, Fields: map[string]value.Value{work.FieldTitle: txt(id)}}
		if parent != "" {
			r.Fields[schema.FieldTaskParent] = txt(parent)
		}
		return r
	}
	const a, b, c, d = "tsk_01a1a5aca7f57659be7320c5f4b70001", "tsk_01a1a5aca7f57659be7320c5f4b70002", "tsk_01a1a5aca7f57659be7320c5f4b70003", "tsk_01a1a5aca7f57659be7320c5f4b70004"
	all := []row.Row{mk(a, ""), mk(b, a), mk(c, b), mk(d, "")}
	got := withDescendants([]row.Row{all[0], all[1]}, all)
	if len(got) != 3 {
		t.Fatalf("with descendants: %d rows, want 3 (a, b, grandchild c; b not twice; d not added)", len(got))
	}
	js, err := exportJSON(got, exportOpt{children: true})
	if err != nil {
		t.Fatal(err)
	}
	var out []struct {
		ID       string `json:"id"`
		Children []struct {
			ID       string `json:"id"`
			Children []struct {
				ID string `json:"id"`
			} `json:"children"`
		} `json:"children"`
	}
	if err := json.Unmarshal([]byte(js), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ID != a || len(out[0].Children) != 1 || out[0].Children[0].ID != b ||
		len(out[0].Children[0].Children) != 1 || out[0].Children[0].Children[0].ID != c {
		t.Fatalf("nesting wrong: %s", js)
	}
}
