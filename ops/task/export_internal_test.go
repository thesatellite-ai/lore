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

// TestExportDateForms (OV53): export writes due/defer_until in a form task
// import reads back: a calendar day as YYYY-MM-DD, a datetime as RFC 3339
// UTC (whatever zone it was given in), nothing when unset; both forms pass
// the import's date check.
func TestExportDateForms(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2026-12-01T00:00:00Z", "2026-12-01"},
		{"2026-12-01T09:30:00Z", "2026-12-01T09:30:00Z"},
		{"2026-12-01T09:30:00+05:30", "2026-12-01T04:00:00Z"},
		{"2026-12-01T00:00:00.5Z", "2026-12-01T00:00:00Z"}, // sub-second: a datetime, not a day
		{"", ""},
	}
	for _, c := range cases {
		r := row.Row{Fields: map[string]value.Value{}}
		if c.in != "" {
			v, err := value.DateTimeOf(c.in)
			if err != nil {
				t.Fatal(err)
			}
			r.Fields[fieldDue] = v
		}
		got := exportDate(r, fieldDue)
		if got != c.want {
			t.Errorf("%q: export %q want %q", c.in, got, c.want)
		}
		if got == "" {
			continue
		}
		rec := importRecord{extID: "x", cols: map[string]string{colTitle: "t", colDue: got}}
		if err := checkRecord(rec); err != nil {
			t.Errorf("%q: import refuses the exported form %q: %v", c.in, got, err)
		}
	}
}
