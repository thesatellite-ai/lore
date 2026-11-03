package main

import (
	"testing"

	"dbent/gen/ent"
	"dbent/schema"
)

// TestPlanDepsSuffixes: a plan's after: takes the same :done / :merged
// suffixes as `task add --after`; without one, same repo waits for done
// and another repo for merged (F9).
func TestPlanDepsSuffixes(t *testing.T) {
	api, app := "repo_api", "repo_app"
	earlier := map[string]*ent.Task{
		"backend": {ID: "tsk_b", RepoID: &api},
		"infra":   {ID: "tsk_i", RepoID: &app},
	}
	cases := []struct {
		name    string
		after   []string
		repo    string
		want    []schema.TaskDependency
		wantErr bool
	}{
		{"plain, same repo", []string{"backend"}, api, []schema.TaskDependency{{TaskID: "tsk_b", Until: schema.DependencyUntilDone}}, false},
		{"plain, other repo", []string{"backend"}, app, []schema.TaskDependency{{TaskID: "tsk_b", Until: schema.DependencyUntilMerged}}, false},
		{":merged, same repo", []string{"backend:merged"}, api, []schema.TaskDependency{{TaskID: "tsk_b", Until: schema.DependencyUntilMerged}}, false},
		{":done, other repo", []string{"backend:done"}, app, []schema.TaskDependency{{TaskID: "tsk_b", Until: schema.DependencyUntilDone}}, false},
		{"two, mixed", []string{"backend:merged", "infra"}, app, []schema.TaskDependency{{TaskID: "tsk_b", Until: schema.DependencyUntilMerged}, {TaskID: "tsk_i", Until: schema.DependencyUntilDone}}, false},
		{"same key twice", []string{"backend", "backend:merged"}, api, []schema.TaskDependency{{TaskID: "tsk_b", Until: schema.DependencyUntilDone}}, false},
		{"unknown key", []string{"frontend"}, api, nil, true},
		{"unknown key with suffix", []string{"frontend:merged"}, api, nil, true},
		{"unknown suffix", []string{"backend:landed"}, api, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := planDeps(planTask{Key: "x", After: tc.after}, earlier, tc.repo)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("deps = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("deps = %+v, want %+v", got, tc.want)
				}
			}
		})
	}
}
