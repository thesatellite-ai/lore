package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestTimeShowCountsCarriedTotals is F-MOVE-3 (te-b's finding on the time
// story): a task moved into a client repo with --with-time totals keeps
// no entries, only time_totals; `task time show` and `task list --min-time`
// count those totals as effort (they gave 0 before), and time logged in
// the client repo since adds to them.
func TestTimeShowCountsCarriedTotals(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	id := add(h, "nightly export", "")
	tid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	tt, err := value.JSONOf([]byte(`{"from":"2026-11-17","to":"2026-11-18","workers":[{"label":"mira","kind":"human","seconds":5400},{"label":"tomas via codex","kind":"agent","seconds":1800}],"effort_seconds":7200}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.App.Update(context.Background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableTasks, ID: tid},
		map[string]value.Value{opstask.FieldTimeTotals: tt}); err != nil {
		t.Fatal(err)
	}
	show := h.Must(opstask.OpTimeShow, `{"id":"`+id+`"}`)
	if js := h.JSON(show); !strings.Contains(js, `"status":"todo","effort_seconds":7200,`) {
		t.Fatalf("effort with carried totals, want 7200: %s", js)
	}
	if js := h.JSON(show); !strings.Contains(js, `"carried_totals"`) {
		t.Fatalf("show --json names the carried totals: %s", js)
	}
	if txt := opstest.Text(show); !strings.Contains(txt, "carried as totals") {
		t.Fatalf("show says where the effort came from: %s", txt)
	}
	h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"30m"}`)
	if js := h.JSON(h.Must(opstask.OpTimeShow, `{"id":"`+id+`"}`)); !strings.Contains(js, `"effort_seconds":9000,`) {
		t.Fatalf("effort after 30m logged here, want 9000: %s", js)
	}
	if js := h.JSON(h.Must(opstask.OpList, `{"min_time":"2h"}`)); !strings.Contains(js, id) {
		t.Fatalf("task list --min-time 2h counts the carried totals: %s", js)
	}
}
