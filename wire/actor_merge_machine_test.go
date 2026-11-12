package wire

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestActorMergeSplitsMachineRows (owner Mac run 2, item 33): rows naming
// the merged actor sit in the repo store (a task) and in the machine store
// (a run: a machine-local table, listed from any store). The bulk write
// puts each store's rows in that store's own commit, so both land; before,
// the run row joined the repo's stage and the whole write failed with
// "one batch cannot write to two stores (repo, machine)", 0 rows written.
func TestActorMergeSplitsMachineRows(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	ctx := background()
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !eng.HasMachineStore() {
		t.Skip("the test app has no machine store")
	}
	seedActor(t, eng, mergeInto, "Una Tester", mergeKey, "una")
	seedActor(t, eng, mergeFrom, "Una Tester", "", "")
	from, err := ids.ParseAny(mergeFrom)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := value.RefOf(from)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := ids.ParseAny(projectID(t, a))
	if err != nil {
		t.Fatal(err)
	}
	task := map[string]value.Value{taskAssigneeField: ref}
	for k, v := range map[string]string{"title": "ship it", "status": "todo", "priority": "medium", "commitment": "accepted"} {
		task[k] = mustText(t, v)
	}
	if task["project_id"], err = value.RefOf(pid); err != nil {
		t.Fatal(err)
	}
	tw, err := eng.Create(ctx, store.SourceRepo, schema.TableTasks, task)
	if err != nil {
		t.Fatal(err)
	}
	run0 := map[string]value.Value{
		schema.FieldRunKind:      mustText(t, "agent"),
		schema.FieldRunStatus:    mustText(t, schema.RunInProgress),
		schema.FieldRunActorID:   mustText(t, mergeFrom),
		schema.FieldRunProjectID: mustText(t, pid.String()),
	}
	rw, err := eng.Create(ctx, store.SourceMachine, schema.TableRuns, run0)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := run(t, a, opsinfra.OpActorMerge, `{"from":"`+mergeFrom+`","into":"`+mergeInto+`"}`, true)
	if err != nil {
		t.Fatalf("merge = %v; want the repo's and the machine's rows each in their own store write", err)
	}
	var out opsinfra.ActorMergeOut
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.Rows != 2 {
		t.Fatalf("merge = %s, %v; want 2 rows (the task and the run)", raw, err)
	}
	if got := assignee(t, a, tw.Row.ID); got != mergeInto {
		t.Errorf("task assignee = %q, want %q", got, mergeInto)
	}
	_, r, err := a.Get(ctx, engine.Ref{Store: store.SourceMachine, Table: schema.TableRuns, ID: rw.Row.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldText(r, schema.FieldRunActorID); got != mergeInto {
		t.Errorf("run actor_id = %q, want %q", got, mergeInto)
	}
}
