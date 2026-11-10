package row_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/row"
)

// TestMoveJSON (later 522): `lore move <id> --to me` asks first
// (E_NEEDS_CONFIRM without --yes, nothing moved), then moves keeping the
// id; --json carries id, table and the target, the text says so, and show
// then finds the row in the target store.
func TestMoveJSON(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	id := h.ID(h.Must("memory.add", `{"body":"to move"}`))
	in := `{"id":"` + id + `","to":"me"}`
	h.Fails(row.OpMove, in, errs.NeedsConfirm)
	if got := showJSON(t, h, `{"id":"`+id+`"}`); got.Store != "repo" {
		t.Fatalf("a refused move moved the row to %s", got.Store)
	}
	res := h.MustYes(row.OpMove, in)
	var out row.MoveOut
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != id || out.Table != "memories" || out.To != "me" {
		t.Fatalf("move json: %s", h.JSON(res))
	}
	if txt := opstest.Text(res); !strings.HasPrefix(txt, "✓ "+id+" moved to store me (same id") {
		t.Fatalf("move text: %q", txt)
	}
	if got := showJSON(t, h, `{"id":"`+id+`"}`); got.Store != "me" || got.Fields["body"] != "to move" {
		t.Fatalf("after move: %+v", got)
	}
}
