package task_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestTaskParentRefsF15 (later 209): a task's mission, task list and plan
// pointers follow F15 like every ref field (the engine checks them at
// write time): from a client repo, add or edit naming an org row is
// E_LEAK_BLOCKED and writes nothing; the same pointer from an org task is
// allowed, and a repo row named from the client repo is allowed.
func TestTaskParentRefsF15(t *testing.T) {
	h := opstest.New(t, repoClient(true), withOrg(t))
	orgMission := h.ID(h.Must("mission.add", `{"title":"org m","to":"`+orgStore+`"}`))
	orgList := h.ID(h.Must("tasklist.add", `{"title":"org l","body":"b","to":"`+orgStore+`"}`))
	orgPlan := h.ID(h.Must("plan.add", `{"title":"org p","body":"b","to":"`+orgStore+`"}`))
	repoMission := h.ID(h.Must("mission.add", `{"title":"repo m"}`))
	repoTask := add(h, "repo task", "")
	cases := []struct{ name, field, id string }{
		{"mission", "mission", orgMission},
		{"tasklist", "tasklist", orgList},
		{"plan", "plan", orgPlan},
	}
	for _, c := range cases {
		before := h.JSON(h.Must(opstask.OpList, `{"all":true}`))
		h.Fails(opstask.OpAdd, `{"title":"x","`+c.field+`":"`+c.id+`"}`, errs.LeakBlocked)
		if h.JSON(h.Must(opstask.OpList, `{"all":true}`)) != before {
			t.Fatalf("%s: a refused add wrote a task", c.name)
		}
		h.Fails(opstask.OpEdit, `{"id":"`+repoTask+`","`+c.field+`":"`+c.id+`"}`, errs.LeakBlocked)
		h.Must(opstask.OpAdd, `{"title":"org side","to":"`+orgStore+`","`+c.field+`":"`+c.id+`"}`)
	}
	h.Must(opstask.OpEdit, `{"id":"`+repoTask+`","mission":"`+repoMission+`"}`)
}
