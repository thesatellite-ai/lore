package mission_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/schema"
)

// TestMissionDoneWarnsOpenTasks: A-17, `lore mission done` with open
// tasks still completes the mission but names the tasks left open and how
// to close them; done and cancelled tasks are not named, and a mission
// with none open prints no warning.
func TestMissionDoneWarnsOpenTasks(t *testing.T) {
	h := opstest.New(t)
	id := addMission(t, h, `{"title":"Ship v3"}`)
	open := h.Seed(schema.TableTasks, map[string]string{"title": `"wire it"`, "status": `"todo"`, "mission_id": opstest.Quote(id)})
	closed := h.Seed(schema.TableTasks, map[string]string{"title": `"old"`, "status": `"done"`, "mission_id": opstest.Quote(id)})
	got := opstest.Text(h.Must("mission.done", `{"id":"`+id+`"}`))
	if !strings.Contains(got, id+" completed") || !strings.Contains(got, "1 task(s) of this mission are still open") ||
		!strings.Contains(got, open+" wire it") || strings.Contains(got, closed) || !strings.Contains(got, "W_OPEN_TASKS") ||
		!strings.Contains(got, "lore task done --id <id>") {
		t.Fatalf("done with an open task: %q", got)
	}
	none := addMission(t, h, `{"title":"Empty"}`)
	if got := opstest.Text(h.Must("mission.done", `{"id":"`+none+`"}`)); strings.Contains(got, "warning") {
		t.Fatalf("done with no open task warned: %q", got)
	}
}
