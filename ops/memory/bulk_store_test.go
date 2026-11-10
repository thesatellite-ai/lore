package memory_test

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// bulkOut is the part of a bulk write's --json answer that names where
// it wrote.
type bulkOut struct {
	ID    string `json:"id"`
	Store string `json:"store"`
	Rows  []struct {
		ID    string `json:"id"`
		Store string `json:"store"`
	} `json:"rows"`
}

// TestBulkWritesNameTheirStore: every bulk (--where) write names the store
// its rows were in, and each row with its store, as a single-row write
// does (they answered only the batch id, with "store": "").
func TestBulkWritesNameTheirStore(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	for _, add := range []struct{ op, in string }{
		{"memory.add", `{"body":"bulk one"}`}, {"memory.add", `{"body":"bulk two"}`},
		{"rule.add", `{"body":"bulk rule one","severity":"should"}`}, {"rule.add", `{"body":"bulk rule two","severity":"should"}`},
		{"task.add", `{"title":"bulk task one","priority":"low"}`}, {"task.add", `{"title":"bulk task two","priority":"low"}`},
		{"plan.add", `{"title":"bulk plan one","body":"b"}`}, {"plan.add", `{"title":"bulk plan two","body":"b"}`},
		{"mission.add", `{"title":"bulk mission one"}`}, {"mission.add", `{"title":"bulk mission two"}`},
	} {
		h.MustYes(add.op, add.in)
	}
	for _, c := range []struct{ op, in string }{
		{"task.edit", `{"where":["priority=low"],"priority":"high"}`},
		{"task.delete", `{"where":["priority=high"]}`},
		{"memory.delete", `{"where":["body~=bulk"]}`},
		{"rule.delete", `{"where":["severity=should"]}`},
		{"plan.delete", `{"where":["title~=bulk plan"]}`},
		{"mission.delete", `{"where":["title~=bulk mission"]}`},
	} {
		var out bulkOut
		if err := json.Unmarshal([]byte(h.JSON(h.MustYes(c.op, c.in))), &out); err != nil {
			t.Fatal(err)
		}
		if out.Store == "" || len(out.Rows) != 2 {
			t.Errorf("%s --where: store %q, rows %+v", c.op, out.Store, out.Rows)
		}
		for _, r := range out.Rows {
			if r.ID == "" || r.Store != out.Store {
				t.Errorf("%s --where: row %+v", c.op, r)
			}
		}
	}
}
