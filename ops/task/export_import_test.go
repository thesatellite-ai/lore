package task_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestExportImportAcrossRepos (OV53, TASK 19): a json export from one repo
// imports into another repo: the same titles, priorities, due dates and
// sub-task shape arrive, and references that only mean something in the
// first repo (its tasklist, mission) do not stop the import.
func TestExportImportAcrossRepos(t *testing.T) {
	a := opstest.New(t, repoClient(false))
	parent := add(a, "Ship invites", `"priority":"high","due":"2026-12-01"`)
	add(a, "Write API", `"parent":"`+parent+`","priority":"low"`)
	add(a, "Café ünïcode", `"body":"multi\nline"`)
	exp := a.Must(opstask.OpExport, `{"format":"json","with_children":true}`)
	var out opstask.ExportOut
	if err := json.Unmarshal([]byte(a.JSON(exp)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Count < 3 {
		t.Fatalf("export count %d: %s", out.Count, out.Content)
	}

	b := opstest.New(t, repoClient(false))
	file := writeFile(t, "tasks.json", out.Content)
	res, err := runImport(b, `{"file":`+opstest.Quote(file)+`}`, true)
	if err != nil {
		t.Fatalf("import into another repo: %v\nfile:\n%s", err, out.Content)
	}
	got := importIDs(b, res)
	if len(got) != 3 {
		t.Fatalf("imported %d tasks, want 3: %s", len(got), b.JSON(res))
	}
	byTitle := map[string]string{}
	for _, id := range got {
		_, r := get(b, schema.TableTasks, id)
		byTitle[field(r, "title")] = id
	}
	_, p := get(b, schema.TableTasks, byTitle["Ship invites"])
	if field(p, "priority") != "high" || !strings.HasPrefix(field(p, "due_at"), "2026-12-01") {
		t.Errorf("parent fields %v", p.Fields)
	}
	_, c := get(b, schema.TableTasks, byTitle["Write API"])
	if field(c, schema.FieldTaskParent) != byTitle["Ship invites"] {
		t.Errorf("child parent %q, want the imported parent %s", field(c, schema.FieldTaskParent), byTitle["Ship invites"])
	}
	_, u := get(b, schema.TableTasks, byTitle["Café ünïcode"])
	if field(u, "body") != "multi\nline" {
		t.Errorf("body %q", field(u, "body"))
	}
	// PC-TASK-1: every dropped reference is one W_IMPORT_DROPPED_REF
	// warning and one note on the task that lost it.
	io, ok := res.Value().Interface().(opstask.ImportOut)
	if !ok {
		t.Fatalf("result is %T", res.Value().Interface())
	}
	if len(io.Warnings) == 0 {
		t.Fatalf("no drop warnings: %s", b.JSON(res))
	}
	notes := 0
	for _, id := range got {
		sh, ok := b.Must(opstask.OpShow, `{"id":"`+id+`"}`).Value().Interface().(opstask.ShowOut)
		if !ok {
			t.Fatal("show result type")
		}
		for _, n := range sh.Data.Notes {
			if strings.Contains(n, "import: dropped ") && strings.Contains(n, " (not in this repo)") {
				notes++
			}
		}
	}
	for _, w := range io.Warnings {
		if w.Code != opstask.WarnImportDroppedRef || !strings.Contains(out.Content, strings.Fields(w.Message)[3]) {
			t.Errorf("warning %+v", w)
		}
	}
	if notes != len(io.Warnings) {
		t.Errorf("%d drop notes for %d warnings", notes, len(io.Warnings))
	}
	if txt := io.Text(); !strings.Contains(txt, opstask.WarnImportDroppedRef+": ") {
		t.Errorf("text has no warning line:\n%s", txt)
	}
	// The import never points at a row of repo a.
	for _, id := range got {
		_, r := get(b, schema.TableTasks, id)
		for _, f := range []string{"tasklist_id", "mission_id", "plan_id"} {
			if v := field(r, f); v != "" && strings.Contains(out.Content, v) {
				t.Errorf("%s %s points at repo a's %s", id, f, v)
			}
		}
	}
}
