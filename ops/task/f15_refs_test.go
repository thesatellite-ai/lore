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

// TestRelateLeakGuardF15 (later 443, TASK 8 leak guard): from a client
// repo, a relation (relates_to, follows_up, duplicates) or a blocked_by
// edge naming an org row is E_LEAK_BLOCKED and writes nothing; the same
// relation between two repo tasks, and from an org task to the repo
// task, is allowed; add and edit --after carry the same edge check.
func TestRelateLeakGuardF15(t *testing.T) {
	h := opstest.New(t, repoClient(true), withOrg(t))
	orgTask := h.ID(h.Must(opstask.OpAdd, `{"title":"org task","to":"`+orgStore+`"}`))
	orgMem := h.ID(h.Must("memory.add", `{"body":"org note","to":"`+orgStore+`"}`))
	repoTask := add(h, "repo task", "")
	other := add(h, "other repo task", "")
	for _, in := range []string{
		`"relates_to":["` + orgTask + `"]`,
		`"relates_to":["` + orgMem + `"]`,
		`"follows_up":["` + orgMem + `"]`,
		`"duplicates":"` + orgTask + `"`,
		`"blocked_by":["` + orgTask + `"]`,
	} {
		before := h.JSON(h.Must(opstask.OpShow, `{"id":"`+repoTask+`","relations":true}`))
		if _, err := h.RunYes(opstask.OpRelate, `{"id":"`+repoTask+`",`+in+`}`); !errs.Has(err, errs.LeakBlocked) {
			t.Errorf("relate %s from the client repo: %v, want E_LEAK_BLOCKED", in, err)
		}
		if after := h.JSON(h.Must(opstask.OpShow, `{"id":"`+repoTask+`","relations":true}`)); after != before {
			t.Errorf("relate %s: a refused relate wrote:\n%s\n---\n%s", in, before, after)
		}
	}
	h.MustYes(opstask.OpRelate, `{"id":"`+repoTask+`","relates_to":["`+other+`"],"blocked_by":["`+other+`"]}`)
	h.MustYes(opstask.OpRelate, `{"id":"`+orgTask+`","relates_to":["`+repoTask+`"]}`)
	// --blocks writes blocked_by on the org task (internal -> client: allowed).
	h.MustYes(opstask.OpRelate, `{"id":"`+repoTask+`","blocks":["`+orgTask+`"]}`)
	// add and edit --after: the same edge check (blocked_by is not a schema ref).
	before := h.JSON(h.Must(opstask.OpList, `{"all":true}`))
	h.Fails(opstask.OpAdd, `{"title":"waits","after":["`+orgTask+`"]}`, errs.LeakBlocked)
	if h.JSON(h.Must(opstask.OpList, `{"all":true}`)) != before {
		t.Error("a refused add --after wrote a task")
	}
	h.Fails(opstask.OpEdit, `{"id":"`+other+`","after":["`+orgTask+`"]}`, errs.LeakBlocked)
	h.Must(opstask.OpAdd, `{"title":"org waits","to":"`+orgStore+`","after":["`+other+`"]}`)
}
