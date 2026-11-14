package host

import (
	"context"
	"sort"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
)

// TestLiveLeakCheckNamesCheckoutPaths: the leak check of a live write sees
// each row under the path it gets in the checkout (.lore/data/<table>/<id>.json,
// below the project folder in a monorepo), as a branch write's rows are
// named, so the checker's row-file rules apply to both alike. On the
// live-tree path (_root/tasks/<id>.json) a live task in a linked client
// repo was read as free text and refused for naming its own author.
func TestLiveLeakCheckNamesCheckoutPaths(t *testing.T) {
	const id = "tsk_0190aaaa0000700080000000000000a1"
	f, err := live.DecodeFile([]byte(`{"_live": {"hlc": "2026-11-03T14:44:58.429Z/0000/x"}, "_table": "tasks", "_v": 1, "id": "` + id + `", "title": "a task"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	h := &liveHost{cfg: LiveConfig{LeakCheck: func(_ context.Context, _ string, docs map[string][]byte) error {
		for p := range docs {
			got = append(got, p)
		}
		return nil
	}}}
	rows := map[live.RowKey]live.File{{Table: "tasks", ID: id}: f}
	for folder, want := range map[string]string{
		live.RootFolder: ".lore/data/tasks/" + id + ".json",
		"apps/web":      "apps/web/.lore/data/tasks/" + id + ".json",
	} {
		got = nil
		if err := h.leakCheck(context.Background(), leakWhereLive, folder, rows); err != nil {
			t.Fatal(err)
		}
		sort.Strings(got)
		if len(got) != 1 || got[0] != want {
			t.Errorf("folder %s: leak check saw %v, want [%s]", folder, got, want)
		}
	}
}
