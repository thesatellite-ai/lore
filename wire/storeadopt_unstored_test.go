package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// adoptKeyMemory is a memory of the adopted repo whose body holds a
// key-shaped value (put in the row file by hand, as an editor would).
const adoptKeyMemory = "mem_01a1f9b0000070008000000000000b01"

// TestStoreAdoptUnstoredAddsRotateTask (devco:D-762, owner P-4): `lore
// store adopt --secret-unstored` redacts the key and adds the
// store-then-rotate task its help promises, in the target folder, with the
// folder's own project row, and says nothing failed. Before, the task step
// ran in the adopt's folder engine, which has no repo: the task asked for
// "this repo's project" (E_NOT_FOUND "no store named repo is attached"),
// and with that fixed the folder had no project row for the task's list to
// name; the run only listed "a store-then-rotate task failed" under "left
// in the repo".
func TestStoreAdoptUnstoredAddsRotateTask(t *testing.T) {
	t.Parallel()
	repo, storeDir, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"northwind","folder":"client_x"}`, true); err != nil {
		t.Fatal(err)
	}
	src := adoptRepo(t)
	key := "AKIA" + strings.Repeat("Q", 4) + "7XKD2M4N5P6R" // built at run time: no key-shaped literal in the repo
	doc := `{"_table": "memories", "_v": 1, "body": "staging token ` + key + `", "created_at": "2026-11-02T10:00:00.000000000Z", "id": "` + adoptKeyMemory +
		`", "kind": "retrieved", "project_id": "` + adoptProject + `", "source_kind": "manual", "trust_score": 0.5, "updated_at": "2026-11-02T10:00:00.000000000Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(src, ".lore", "data", "memories", adoptKeyMemory+".json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	e, ok := a.Registry().Lookup(opsstores.OpAdopt)
	if !ok {
		t.Fatal("no store.adopt op")
	}
	res, err := a.Run(background(), e, op.JSONDecoder{Body: []byte(`{"target":"northwind/client_x","from":"` + filepath.ToSlash(src) + `"}`)},
		op.Caller{Yes: true, Secret: op.SecretChoice{Unstored: true}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out opsstores.AdoptOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	for _, l := range out.Left {
		if strings.Contains(l, "store-then-rotate") {
			t.Fatalf("the adopt reports the task failed: %q\n%s", l, out.Text())
		}
	}
	files := treeSums(t, storeDir)
	mem, err := os.ReadFile(filepath.Join(storeDir, "client_x", "memories", adoptKeyMemory+".json"))
	if err != nil || strings.Contains(string(mem), key) || !strings.Contains(string(mem), "${secret:unstored}") {
		t.Fatalf("the adopted memory is not redacted (%v):\n%s", err, mem)
	}
	var task, project string
	for p := range files {
		switch {
		case strings.HasPrefix(p, "client_x/tasks/"):
			b, _ := os.ReadFile(filepath.Join(storeDir, filepath.FromSlash(p)))
			if strings.Contains(string(b), "secret-unstored-"+adoptKeyMemory) {
				task = string(b)
			}
		case strings.HasPrefix(p, "client_x/projects/"):
			project = p
		}
	}
	if task == "" {
		t.Fatalf("no store-then-rotate task for %s in the folder: %v", adoptKeyMemory, files)
	}
	if project == "" || !strings.Contains(task, strings.TrimSuffix(filepath.Base(project), ".json")) {
		t.Fatalf("the task does not name the folder's project row (%q):\n%s", project, task)
	}
}
