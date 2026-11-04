// plan_apply.go — what is left of v0.1.17's `lore plan apply`: the
// planDeps rule (after: keys with :done / :merged) that
// team_fx_planafter_test.go pins. The command itself moved to the core as
// `lore mission apply` (TSQ9, ops/mission/apply.go, same rule); this
// helper stays until that test is ported to ops/mission.
package main

import (
	"strings"

	"dbent/gen/ent"
	"dbent/schema"
	"saas/pkg/aicoder/errcodes"
)

type planTask struct {
	Key     string   `yaml:"key"`
	Title   string   `yaml:"title"`
	Repo    string   `yaml:"repo"`
	After   []string `yaml:"after"`
	Timebox string   `yaml:"timebox"`
	Role    string   `yaml:"role"`
	Gate    string   `yaml:"gate"`
}

// planDeps turns a plan task's after: entries into dependency edges. An
// entry is an earlier task's key, optionally with the same :done / :merged
// suffix `task add --after <id>[:merged]` takes (depSuffixes). Without a
// suffix it waits for done when both tasks are in the same repo and for
// merged otherwise (F9); a suffix overrides that, e.g. backend:merged makes
// a same-repo task wait until the backend code is on main.
func planDeps(pt planTask, earlier map[string]*ent.Task, repoID string) ([]schema.TaskDependency, error) {
	var deps []schema.TaskDependency
	seen := map[string]bool{}
	for _, raw := range pt.After {
		k := strings.TrimSpace(raw)
		until := schema.DependencyUntil("")
		for suf, u := range depSuffixes {
			if base, ok := strings.CutSuffix(k, suf); ok {
				k, until = base, u
			}
		}
		up, ok := earlier[k]
		if !ok {
			return nil, errcodes.New(errcodes.InvalidInput, "plan: task "+pt.Key+" is after "+raw+", which is not an earlier task of the plan").
				WithHint("after: takes keys of earlier tasks, optionally with :done or :merged (e.g. backend:merged)")
		}
		if until == "" {
			until = schema.DependencyUntilDone
			if deref(up.RepoID) != repoID {
				until = schema.DependencyUntilMerged
			}
		}
		if seen[up.ID] {
			continue
		}
		seen[up.ID] = true
		deps = append(deps, schema.TaskDependency{TaskID: up.ID, Until: until})
	}
	return deps, nil
}
