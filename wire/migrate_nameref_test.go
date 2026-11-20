package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// Made-up ids of the name-ref fixtures (D-766).
const (
	nrList   = "tlt_01a1cb5717d278538732fc6e305a2001"
	nrNamed  = "tsk_01a1cb5717d278538732fc6e305a2002"
	nrNoList = "tsk_01a1cb5717d278538732fc6e305a2003"
)

// nrRows writes the v0.1.17 shape of finding H-25 into repo's data folder:
// a list "Backlog", a task whose tasklist_id is "backlog" (the list's
// name, not its id) and one whose tasklist_id names no list.
func nrRows(t *testing.T, repo, prj string) {
	t.Helper()
	v1Row(t, repo, schema.TableTasklists, nrList, `"project_id":"`+prj+`","title":"Backlog"`)
	task := `"project_id":"` + prj + `","status":"todo","priority":"medium","commitment":"accepted"`
	v1Row(t, repo, schema.TableTasks, nrNamed, task+`,"title":"order paper","tasklist_id":"backlog"`)
	v1Row(t, repo, schema.TableTasks, nrNoList, task+`,"title":"renew the domain","tasklist_id":"someday"`)
}

// TestMigrateRunNameRefs (devco D-766, H-25): migrate run writes a task
// whose tasklist_id held a list's name with that list's id, clears one
// that names no list, says both in its steps; the rewrite is not picked
// up as a hand edit, and the task takes an edit afterwards (it was
// refused "field tasklist_id: want a ref value").
func TestMigrateRunNameRefs(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	nrRows(t, repo, projectID(t, a))
	gitT(t, repo, "add", "-A") // committed, as a 0.1.17 project is: the hand-edit pick-up compares with HEAD
	gitT(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "v0.1.17 rows")

	raw, err := run(t, a, opsmigrate.OpRun, `{}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var out opsmigrate.RunOut
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	steps := strings.Join(out.Steps, "\n")
	for _, want := range []string{
		"warning: tasks/" + nrNamed + `: tasklist_id "backlog" is a name, not an id; now the task_lists row ` + nrList,
		"warning: tasks/" + nrNoList + `: tasklist_id "someday" is a name, not an id; cleared: no task_lists row has that name`,
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("steps lack %q:\n%s", want, steps)
		}
	}
	named := rowFile(t, filepath.Join(repo, ".lore", "data", "tasks", nrNamed+".json"))
	if fieldString(t, named, "tasklist_id") != nrList || string(named["_v"]) != "2" {
		t.Fatalf("the named task: %v", named)
	}
	if none := rowFile(t, filepath.Join(repo, ".lore", "data", "tasks", nrNoList+".json")); none["tasklist_id"] != nil {
		t.Fatalf("the task naming no list kept tasklist_id: %s", none["tasklist_id"])
	}
	path := filepath.Join(repo, ".lore", "data", "tasks", nrNamed+".json")
	before := string(mustRead(t, path))
	var mu sync.Mutex
	var said []string
	b := newTestAppWith(t, repo, func(o *Options) {
		o.Notice = func(m string) { mu.Lock(); said = append(said, m); mu.Unlock() }
	})
	if _, r, err := b.Get(background(), engineRef(schema.TableTasks, nrNamed)); err != nil || r.Deleted {
		t.Fatalf("read after the run: %v", err)
	}
	mu.Lock()
	for _, l := range said {
		if strings.Contains(l, "hand edit") {
			t.Errorf("the migration's tasklist_id was taken for a hand edit: %q", l)
		}
	}
	mu.Unlock()
	if after := string(mustRead(t, path)); after != before {
		t.Fatalf("a read re-stamped the migrated task as a hand edit:\n%s\n%s", before, after)
	}
	if _, err := run(t, b, opstask.OpEdit, `{"id":"`+nrNamed+`","title":"order paper (A4)"}`, true); err != nil {
		t.Fatalf("edit after the run: %v", err)
	}
}

// TestStoreAdoptNameRefs (D-766): store adopt of the same v0.1.17 repo
// writes the list's id, clears the unknown name, and says both in
// out.refs (printed as warning lines).
func TestStoreAdoptNameRefs(t *testing.T) {
	t.Parallel()
	repo, storeDir, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"northwind","folder":"client_y"}`, true); err != nil {
		t.Fatal(err)
	}
	src := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(src, ".lore", "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".lore", "data", "_meta.json"), []byte(`{"_v": 1, "project_id": "`+adoptProject+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	v1Row(t, src, schema.TableProjects, adoptProject, `"name":"widgets"`)
	nrRows(t, src, adoptProject)

	out, err := adoptRun(t, a, `{"target":"northwind/client_y","from":"`+filepath.ToSlash(src)+`"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	refs := strings.Join(out.Refs, "\n")
	if len(out.Refs) != 2 || !strings.Contains(refs, `tasklist_id "backlog" is a name, not an id; now the task_lists row `+nrList) ||
		!strings.Contains(refs, `tasklist_id "someday" is a name, not an id; cleared`) {
		t.Fatalf("out.refs = %q", out.Refs)
	}
	if !strings.Contains(out.Text(), "  warning: tasks/"+nrNoList) {
		t.Fatalf("text lacks the warning:\n%s", out.Text())
	}
	folder := filepath.Join(storeDir, "client_y")
	if named := rowFile(t, filepath.Join(folder, "tasks", nrNamed+".json")); fieldString(t, named, "tasklist_id") != nrList {
		t.Fatalf("the adopted named task: %v", named)
	}
	if none := rowFile(t, filepath.Join(folder, "tasks", nrNoList+".json")); none["tasklist_id"] != nil {
		t.Fatalf("the adopted task naming no list kept tasklist_id: %s", none["tasklist_id"])
	}
}
