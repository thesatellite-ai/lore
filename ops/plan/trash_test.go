package plan_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// TestPlanDeleteTrashesTasks (DELETION 3.4): a plan's tasks go to the
// trash with it (tasks.plan_id is Cascade, no longer SetNull).
func TestPlanDeleteTrashesTasks(t *testing.T) {
	h := opstest.New(t)
	id := planID(t, h, "Doomed")
	child := h.Seed(schema.TableTasks, map[string]string{"plan_id": opstest.Quote(id)})
	h.MustYes("plan.delete", `{"id":"`+id+`"}`)
	h.Fails("task.show", `{"id":"`+child+`"}`, errs.InTrash)
}

// TestPlanListDeletedFlags (DELETION 3.2): --include-deleted lists the
// trashed plan beside the live one and marks it; --deleted-only lists it
// alone; with neither it stays hidden.
func TestPlanListDeletedFlags(t *testing.T) {
	h := opstest.New(t)
	live := planID(t, h, "Kept")
	gone := planID(t, h, "Gone")
	h.MustYes("plan.delete", `{"id":"`+gone+`"}`)
	if got := opstest.Text(h.Must("plan.list", ``)); strings.Contains(got, gone) || !strings.Contains(got, live) {
		t.Fatalf("plain list %q", got)
	}
	got := opstest.Text(h.Must("plan.list", `{"include_deleted":true}`))
	if !strings.Contains(got, live) || !strings.Contains(got, gone) || !strings.Contains(got, "in the trash: "+gone) {
		t.Fatalf("--include-deleted %q", got)
	}
	got = opstest.Text(h.Must("plan.list", `{"deleted_only":true}`))
	if strings.Contains(got, live+"\n") || !strings.Contains(got, gone) {
		t.Fatalf("--deleted-only %q", got)
	}
}

func planID(t *testing.T, h *opstest.H, title string) string {
	t.Helper()
	var out struct{ Data struct{ ID string } }
	if err := json.Unmarshal([]byte(h.JSON(h.Must("plan.add", `{"title":"`+title+`","body":"b"}`))), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data.ID
}
