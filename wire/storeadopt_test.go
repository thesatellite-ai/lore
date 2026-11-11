package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// The repo adopted in TestStoreAdopt (v0.1.17 shapes): its project, a
// tag, a tagged memory (also on the live ref, newer there), a parent and
// a child task, a comment on the child, a task and a mission that are
// only on the live ref (the mission stamped by an agent).
const (
	adoptProject  = "prj_01a1cb57169c7c73a3a5dd7f0e1b8c6d"
	adoptTag      = "tag_1092b25e5b3072429ed1bccebcf4af8b"
	adoptLink     = "etg_01a1cb57169c7c73a3a5dd7f0e1b8c70"
	adoptMemory   = "mem_01a1cb5743e371b7bab946c6cf489ddf"
	adoptParent   = "tsk_01a1cb5717d278538732fc6e305a1002"
	adoptChild    = "tsk_01a1cb5717d278538732fc6e305a1003"
	adoptComment  = "cmt_01a1af108f8670868b2d8157445678ee"
	adoptLiveTask = "tsk_01a1cb5717d278538732fc6e305a1004"
	adoptMission  = "msn_01a1cb5718e87690996bbbb6edb76879"
	adoptLiveHLC  = "2026-11-05T09:00:00.000Z/0000/" + otherActor
)

// adoptRepo writes the adopted repo (see the constants) into a new git repo.
func adoptRepo(t *testing.T) string {
	t.Helper()
	repo := gitRepo(t)
	files := map[string]string{
		"_meta.json":                         `{"_v": 1, "project_id": "` + adoptProject + `"}`,
		"projects/" + adoptProject + ".json": `{"_table": "projects", "_v": 1, "created_at": "2026-11-01T09:00:00.000000000Z", "id": "` + adoptProject + `", "name": "widgets", "updated_at": "2026-11-01T09:00:00.000000000Z"}`,
		"tags/" + adoptTag + ".json":         `{"_table": "tags", "_v": 1, "created_at": "2026-11-01T09:00:00.000000000Z", "id": "` + adoptTag + `", "name": "ops", "project_id": "` + adoptProject + `", "updated_at": "2026-11-01T09:00:00.000000000Z"}`,
		"entity_tags/" + adoptLink + ".json": `{"_table": "entity_tags", "_v": 1, "created_at": "2026-11-01T09:00:00.000000000Z", "entity_id": "` + adoptMemory + `", "entity_table": "memories", "id": "` + adoptLink + `", "tag_id": "` + adoptTag + `", "updated_at": "2026-11-01T09:00:00.000000000Z"}`,
		"memories/" + adoptMemory + ".json":  `{"_table": "memories", "_v": 1, "body": "the cache warms at boot", "created_at": "2026-11-02T10:00:00.000000000Z", "id": "` + adoptMemory + `", "kind": "retrieved", "project_id": "` + adoptProject + `", "source_kind": "manual", "trust_score": 0.5, "updated_at": "2026-11-02T10:00:00.000000000Z"}`,
		"tasks/" + adoptParent + ".json":     `{"_table": "tasks", "_v": 1, "attempts": 0, "claim_epoch": 0, "commitment": "accepted", "created_at": "2026-11-02T10:00:00.000000000Z", "id": "` + adoptParent + `", "priority": "medium", "project_id": "` + adoptProject + `", "status": "todo", "title": "ship billing", "updated_at": "2026-11-02T10:00:00.000000000Z"}`,
		"tasks/" + adoptChild + ".json":      `{"_table": "tasks", "_v": 1, "attempts": 0, "claim_epoch": 0, "commitment": "accepted", "created_at": "2026-11-02T10:00:00.000000000Z", "id": "` + adoptChild + `", "parent_id": "` + adoptParent + `", "priority": "medium", "project_id": "` + adoptProject + `", "status": "todo", "title": "write the invoice job", "updated_at": "2026-11-02T10:00:00.000000000Z"}`,
		"comments/" + adoptComment + ".json": `{"_table": "comments", "_v": 1, "body": "agreed", "created_at": "2026-11-02T10:00:00.000000000Z", "entity_id": "` + adoptChild + `", "entity_table": "tasks", "id": "` + adoptComment + `", "updated_at": "2026-11-02T10:00:00.000000000Z"}`,
	}
	for p, doc := range files {
		full := filepath.Join(repo, ".lore", "data", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(doc+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeLiveRef(t, repo, liveRefLocal, map[string]string{
		livePath(adoptMemory): liveMemory(adoptMemory, adoptProject, "the cache warms at boot, then sleeps", adoptLiveHLC),
		liveRootFolder + "/tasks/" + adoptLiveTask + ".json": `{"_table":"tasks","_v":1,"attempts":0,"claim_epoch":0,"commitment":"accepted","created_at":"2026-11-03T10:00:00.000000000Z","id":"` + adoptLiveTask +
			`","priority":"high","project_id":"` + adoptProject + `","status":"in_progress","title":"open on the live ref","updated_at":"2026-11-03T10:00:00.000000000Z","_live":{"hlc":"` + adoptLiveHLC + `"}}`,
		liveRootFolder + "/missions/" + adoptMission + ".json": liveMission(adoptMission, adoptProject, "ship the importer", liveAgentStamp),
	})
	return repo
}

// treeSums is every file under dir (slash path → sha256), .git and lore's
// own lock aside.
func treeSums(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() && rel == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Base(p) == "lock" {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		s := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(s[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// adoptRun runs store.adopt and decodes its answer.
func adoptRun(t *testing.T, a *App, in string, yes bool) (opsstores.AdoptOut, error) {
	t.Helper()
	raw, err := run(t, a, opsstores.OpAdopt, in, yes)
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	var out opsstores.AdoptOut
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// TestStoreAdopt: a v0.1.17 repo adopted into a folder store's folder.
// The dry run and a run without a yes write nothing; the run writes every
// row but the project row, with ids, HLCs and relations kept (tags folded
// into tag_ids, parent_id, the comment's target), project_id moved to the
// folder's project, the live-only rows in (the agent's stamp mapped) and
// the newer live copy merged; the repo is left byte for byte as it was. A
// second run writes nothing and the folder's files are identical; a row
// edited in the repo since is written again (and only it);
// --remove-source deletes the adopted row files and keeps the project's.
func TestStoreAdopt(t *testing.T) {
	t.Parallel()
	repo, storeDir, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"northwind","folder":"client_x"}`, true); err != nil {
		t.Fatal(err)
	}
	src := adoptRepo(t)
	srcBefore := treeSums(t, src)
	in := `{"target":"northwind/client_x","from":"` + filepath.ToSlash(src) + `"}`
	folder := filepath.Join(storeDir, "client_x", ".lore", "data")
	storeBefore := treeSums(t, storeDir)

	dry, err := adoptRun(t, a, `{"target":"northwind/client_x","from":"`+filepath.ToSlash(src)+`","dry_run":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	// tag, memory, two tasks, comment from the files; task, mission from the live ref
	if dry.Written != 7 || dry.Live != 3 || !dry.DryRun || !slices.Contains(dry.Left, "projects: 1 row (the folder is its own project)") {
		t.Fatalf("dry run: %+v", dry)
	}
	if _, err = adoptRun(t, a, in, false); !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "copy 7 rows") {
		t.Fatalf("without a yes: %v", err)
	}
	if after := treeSums(t, storeDir); !mapsEqual(after, storeBefore) {
		t.Fatal("the dry run or the unconfirmed run wrote into the store")
	}

	out, err := adoptRun(t, a, in, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Written != 7 || out.DryRun || out.ProjectID == "" {
		t.Fatalf("adopt: %+v", out)
	}
	mem := rowFile(t, filepath.Join(folder, "memories", adoptMemory+".json"))
	if fieldString(t, mem, "project_id") != out.ProjectID || !strings.Contains(string(mem["tag_ids"]), adoptTag) ||
		fieldString(t, mem, "body") != "the cache warms at boot, then sleeps" || fieldString(t, mem, "_hlc") != adoptLiveHLC {
		t.Fatalf("the adopted memory: %v", mem)
	}
	child := rowFile(t, filepath.Join(folder, "tasks", adoptChild+".json"))
	if fieldString(t, child, "parent_id") != adoptParent || string(child["_v"]) != "2" {
		t.Fatalf("the adopted child task: %v", child)
	}
	if c := rowFile(t, filepath.Join(folder, "comments", adoptComment+".json")); fieldString(t, c, "entity_id") != adoptChild {
		t.Fatalf("the adopted comment: %v", c)
	}
	actor, _ := LiveWriterActor("agent")
	if m := rowFile(t, filepath.Join(folder, "missions", adoptMission+".json")); fieldString(t, m, "_hlc") != "2026-11-03T14:44:58.429Z/0000/"+actor.String() {
		t.Fatalf("the agent's mission: %v", m)
	}
	for _, gone := range []string{"projects/" + adoptProject + ".json", "entity_tags/" + adoptLink + ".json"} {
		if _, serr := os.Stat(filepath.Join(folder, filepath.FromSlash(gone))); !os.IsNotExist(serr) {
			t.Errorf("%s reached the folder: %v", gone, serr)
		}
	}
	if after := treeSums(t, src); !mapsEqual(after, srcBefore) {
		t.Fatal("adopt changed the source repo")
	}

	adopted := treeSums(t, storeDir)
	again, err := adoptRun(t, a, in, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Written != 0 || again.Unchanged != 7 || again.DryRun {
		t.Fatalf("second run: %+v", again)
	}
	if after := treeSums(t, storeDir); !mapsEqual(after, adopted) {
		t.Fatal("a second run changed the folder's files")
	}

	// the repo moved on: the parent task edited later (a v1 file, newer updated_at)
	p := filepath.Join(src, ".lore", "data", "tasks", adoptParent+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(strings.Replace(string(b), "ship billing", "ship billing v2", 1), `"updated_at": "2026-11-02T10:00:00.000000000Z"`, `"updated_at": "2026-11-09T10:00:00.000000000Z"`, 1)
	if err = os.WriteFile(p, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := adoptRun(t, a, in, true)
	if err != nil {
		t.Fatal(err)
	}
	if third.Written != 1 || third.Unchanged != 6 || len(third.Tables) != 1 || third.Tables[0].Table != "tasks" {
		t.Fatalf("after an edit: %+v", third)
	}
	if got := rowFile(t, filepath.Join(folder, "tasks", adoptParent+".json")); fieldString(t, got, "title") != "ship billing v2" {
		t.Fatalf("the edited task: %v", got)
	}

	rm, err := adoptRun(t, a, `{"target":"northwind/client_x","from":"`+filepath.ToSlash(src)+`","remove_source":true}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if rm.Written != 0 || rm.Removed != 6 { // tag, memory, two tasks, comment and the entity_tags link (the live-only rows have no file)
		t.Fatalf("--remove-source: %+v", rm)
	}
	for _, keep := range []string{"_meta.json", "projects/" + adoptProject + ".json"} {
		if _, serr := os.Stat(filepath.Join(src, ".lore", "data", filepath.FromSlash(keep))); serr != nil {
			t.Errorf("--remove-source removed %s: %v", keep, serr)
		}
	}
	if _, serr := os.Stat(filepath.Join(src, ".lore", "data", "memories", adoptMemory+".json")); !os.IsNotExist(serr) {
		t.Errorf("the adopted memory file is still in the repo: %v", serr)
	}
}

// TestStoreAdoptRefusals: each refusal names its cause and the next step,
// and writes nothing.
func TestStoreAdoptRefusals(t *testing.T) {
	t.Parallel()
	repo, _, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	src := adoptRepo(t)
	for _, c := range []struct{ in, want string }{
		{`{"target":"northwind"}`, "names no folder; give <store>/<folder>"},
		{`{"target":"northwind/client_y","from":"` + filepath.ToSlash(src) + `"}`, "has no folder client_y; nothing written"},
		{`{"target":"northwind/org","from":"` + filepath.ToSlash(src) + `"}`, "is the org folder"},
		{`{"target":"northwind/acme","from":"` + filepath.ToSlash(t.TempDir()) + `"}`, "is not a git repo"},
	} {
		_, err := adoptRun(t, a, c.in, true)
		var e *errs.Error
		if err == nil || !errors.As(err, &e) || !strings.Contains(e.Error()+" "+e.Hint, c.want) || e.Hint == "" {
			t.Errorf("%s: %v, want %q", c.in, err, c.want)
		}
	}
}

// mapsEqual compares two string maps.
func mapsEqual(x, y map[string]string) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}
