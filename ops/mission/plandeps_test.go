package mission

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// earlierTask is a task row of an earlier plan step in repo (rep_ id).
func earlierTask(t *testing.T, id, repo string) row.Row {
	t.Helper()
	tid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	rid, err := ids.ParseAny(repo)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := value.RefOf(rid)
	if err != nil {
		t.Fatal(err)
	}
	return row.Row{Table: schema.TableTasks, ID: tid, Fields: map[string]value.Value{work.FieldRepo: ref}}
}

// TestPlanDepsSuffixes is saas/cmd/cli team_fx_planafter_test.go
// TestPlanDepsSuffixes ported to mission apply (later.md g3): a plan's
// after: takes the same :done / :merged suffixes as `task add --after`;
// without one, same repo waits for done and another repo for merged (F9).
// Assertions unchanged; only the setup moved from ent tasks to core rows.
func TestPlanDepsSuffixes(t *testing.T) {
	const (
		api, app = "rep_01a1a5aca7f57659be7320c5f4b700a1", "rep_01a1a5aca7f57659be7320c5f4b700a2"
		tskB     = "tsk_01a1a5aca7f57659be7320c5f4b700b1"
		tskI     = "tsk_01a1a5aca7f57659be7320c5f4b700b2"
	)
	earlier := map[string]row.Row{"backend": earlierTask(t, tskB, api), "infra": earlierTask(t, tskI, app)}
	cases := []struct {
		name    string
		after   []string
		repo    string
		want    []dep
		wantErr bool
	}{
		{"plain, same repo", []string{"backend"}, api, []dep{{tskB, untilDone}}, false},
		{"plain, other repo", []string{"backend"}, app, []dep{{tskB, untilMerged}}, false},
		{"plain, no repo", []string{"backend"}, "", []dep{{tskB, untilMerged}}, false},
		{":merged, same repo", []string{"backend:merged"}, api, []dep{{tskB, untilMerged}}, false},
		{":done, other repo", []string{"backend:done"}, app, []dep{{tskB, untilDone}}, false},
		{"two, mixed", []string{"backend:merged", "infra"}, app, []dep{{tskB, untilMerged}, {tskI, untilDone}}, false},
		{"same key twice", []string{"backend", "backend:merged"}, api, []dep{{tskB, untilDone}}, false},
		{"padded key", []string{"  backend  "}, api, []dep{{tskB, untilDone}}, false},
		{"none", nil, api, nil, false},
		{"unknown key", []string{"frontend"}, api, nil, true},
		{"unknown key with suffix", []string{"frontend:merged"}, api, nil, true},
		{"unknown suffix", []string{"backend:landed"}, api, nil, true},
		{"empty key", []string{""}, api, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := planDeps(FileTask{Key: "x", After: tc.after}, earlier, tc.repo)
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
