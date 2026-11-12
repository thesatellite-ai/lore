package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
)

// bulkTaskStem is the id stem of the fixture tasks (placeholders).
const bulkTaskStem = "tsk_01a1cb5d849374588a72"

// bulkTasks is how many fixture tasks name the merged actor.
const bulkTasks = 300

// writeOldTask writes one task row file into repo the way an adopt by an
// older lore left lore 0.1.17's data: tasklist_id "" (0.1.17's "none"),
// made and assigned by actor (assignee "" when empty).
func writeOldTask(t *testing.T, repo, id, project, actor, assignee, status string) {
	t.Helper()
	doc := `{"_hlc": "2026-11-03T14:44:00.000Z/0000/", "_table": "tasks", "_v": 2, "assigned_to_actor_id": "` + assignee + `", "attempts": 0, "claim_epoch": 0, "commitment": "accepted", ` +
		`"created_at": "2026-11-03T14:44:00.000000000Z", "created_by": "` + actor + `", "created_by_actor_id": "` + actor + `", "id": "` + id + `", "priority": "medium", "project_id": "` + project +
		`", "status": "` + status + `", "tasklist_id": "", "title": "old task", "updated_at": "2026-11-03T14:44:00.000000000Z", "updated_by": "` + actor + `"}` + "\n"
	p := filepath.Join(repo, ".lore", "data", string(schema.TableTasks), id+".json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestActorMergeBulk is the owner's repair of adopted v0.1.17 data
// (2026-11-12): tasks with tasklist_id "" (and one assignee "") are edited
// like any other row; a row the merge cannot write does not stop the
// others: it is listed with its reason and fix, the command fails, and a
// rerun writes only what is left; reads by the kept id find the rows the
// merged id made keep it in their history stamps.
func TestActorMergeBulk(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	ctx := background()
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seedActor(t, eng, mergeInto, "Una Tester", mergeKey, "una")
	seedActor(t, eng, mergeFrom, "Una Tester", "", "")
	project := projectID(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	for i := range bulkTasks {
		assignee := mergeFrom
		if i == 0 {
			assignee = "" // v0.1.17's unassigned
		}
		writeOldTask(t, repo, fmt.Sprintf("%s%012x", bulkTaskStem, i), project, mergeFrom, assignee, "todo")
	}
	bad := fmt.Sprintf("%s%012x", bulkTaskStem, 0xbad)
	writeOldTask(t, repo, bad, project, mergeFrom, mergeFrom, "bogus") // a status no lore writes: the edit is refused
	a = newTestApp(t, repo)

	in := `{"from":"` + mergeFrom + `","into":"` + mergeInto + `"}`
	start := time.Now()
	_, err = run(t, a, opsinfra.OpActorMerge, in, true)
	took := time.Since(start)
	if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "1 row could not be written") || !strings.Contains(err.Error(), "tasks "+bad) ||
		!strings.Contains(err.Error(), "field status") || !strings.Contains(err.Error(), "fix: ") {
		t.Fatalf("merge with one bad row = %v; want E_REFUSED naming the row, its reason and its fix", err)
	}
	t.Logf("merge of %d rows took %s", bulkTasks, took)
	for _, i := range []int{0, 1, bulkTasks - 1} {
		id := fmt.Sprintf("%s%012x", bulkTaskStem, i)
		pid, perr := ids.ParseAny(id)
		if perr != nil {
			t.Fatal(perr)
		}
		want := mergeInto
		if i == 0 {
			want = "" // it named nobody: its created_by_actor_id was edited
		}
		if got := assignee(t, a, pid); got != want {
			t.Errorf("%s assignee = %q, want %q (the bad row stopped the merge?)", id, got, want)
		}
		b, rerr := os.ReadFile(filepath.Join(repo, ".lore", "data", string(schema.TableTasks), id+".json"))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if strings.Contains(string(b), `"tasklist_id": ""`) || strings.Contains(string(b), `"assigned_to_actor_id": ""`) {
			t.Errorf("%s still holds an empty-string ref after its edit:\n%s", id, b)
		}
	}

	// the rerun writes only what is left: the bad row again, nothing else
	raw, err := run(t, a, opsinfra.OpActorMerge, in+``, true)
	if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "wrote 0 rows") {
		t.Fatalf("rerun = %s, %v; want only the bad row left", raw, err)
	}
	writeOldTask(t, repo, bad, project, mergeFrom, mergeFrom, "todo") // the person fixes it
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = newTestApp(t, repo)
	raw, err = run(t, a, opsinfra.OpActorMerge, in, true)
	if err != nil {
		t.Fatal(err)
	}
	var out opsinfra.ActorMergeOut
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.Rows != 1 {
		t.Fatalf("merge after the fix = %s, %v; want the one row left", raw, err)
	}

	// history stamps keep the merged id (a repo store keeps no merged_into
	// on its actor copy, F18: the org journey checks that --created-by the
	// kept id follows the roster's mark)
	listed, err := run(t, a, "task.list", `{"created_by":"`+mergeFrom+`","all":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(listed, bulkTaskStem); n != bulkTasks+1 {
		t.Errorf("task list --created-by %s lists %d tasks, want %d: the history stamps must keep it", mergeFrom, n, bulkTasks+1)
	}
}
