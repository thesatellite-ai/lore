package task_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestExportImportAcrossRepos (OV53, TASK 19, G-DECIDE-5): a json export
// from one repo imports into another repo: the same titles, priorities,
// due dates and sub-task shape arrive. Each tasklist / mission / plan
// reference travels as its id and its name: the id when the target holds
// it, else the one row of that name in the target, else it is dropped
// with one W_IMPORT_DROPPED_REF warning and one note line naming why (no
// row of that name, or several). The other repo's default list lands in
// this repo's default list without a warning.
func TestExportImportAcrossRepos(t *testing.T) {
	t.Parallel()
	a := opstest.New(t, repoClient(false))
	list := a.ID(a.Must("tasklist.add", `{"title":"Backlog","body":"b"}`))
	mis := a.ID(a.Must("mission.add", `{"title":"Launch"}`))
	plan := a.ID(a.Must("plan.add", `{"title":"Q4","body":"b"}`))
	parent := add(a, "Ship invites", `"priority":"high","due":"2026-12-01","tasklist":"`+list+`","mission":"`+mis+`","plan":"`+plan+`"`)
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
	for _, w := range []string{`"tasklist_name": "Backlog"`, `"mission_name": "Launch"`, `"plan_name": "Q4"`, `"tasklist_name": "default"`} {
		if !strings.Contains(out.Content, w) {
			t.Errorf("export has no %s:\n%s", w, out.Content)
		}
	}
	file := writeFile(t, "tasks.json", out.Content)

	// Back into the same repo: every id is there, nothing dropped.
	same, err := runImport(a, `{"file":`+opstest.Quote(file)+`}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if so := importOut(t, same); len(so.Warnings) != 0 {
		t.Errorf("same-repo import dropped: %+v", so.Warnings)
	}

	b := opstest.New(t, repoClient(false))
	bList := b.ID(b.Must("tasklist.add", `{"title":"Backlog","body":"b"}`))
	b.Must("mission.add", `{"title":"Launch"}`)
	b.Must("mission.add", `{"title":"Launch"}`)
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
	if field(p, "tasklist_id") != bList || field(p, "mission_id") != "" || field(p, "plan_id") != "" {
		t.Errorf("parent refs: tasklist %q (want %s by name), mission %q, plan %q (want both dropped)",
			field(p, "tasklist_id"), bList, field(p, "mission_id"), field(p, "plan_id"))
	}
	_, c := get(b, schema.TableTasks, byTitle["Write API"])
	if field(c, schema.FieldTaskParent) != byTitle["Ship invites"] {
		t.Errorf("child parent %q, want the imported parent %s", field(c, schema.FieldTaskParent), byTitle["Ship invites"])
	}
	_, u := get(b, schema.TableTasks, byTitle["Café ünïcode"])
	if field(u, "body") != "multi\nline" {
		t.Errorf("body %q", field(u, "body"))
	}
	if l := field(u, "tasklist_id"); l == "" || strings.Contains(out.Content, l) {
		t.Errorf("default-list task: tasklist %q, want b's own default list", l)
	}
	io := importOut(t, res)
	wantWarn := map[string]string{
		"mission_id": "dropped mission_id " + mis + ` (not in this repo; 2 missions named "Launch")`,
		"plan_id":    "dropped plan_id " + plan + ` (not in this repo; no plan named "Q4")`,
	}
	// The parent and its sub-task (which inherited both) lose the two.
	if len(io.Warnings) != 2*len(wantWarn) {
		t.Errorf("warnings %+v, want %d", io.Warnings, 2*len(wantWarn))
	}
	for _, w := range io.Warnings {
		if w.Code != opstask.WarnImportDroppedRef || !strings.HasSuffix(w.Message, wantWarn[w.Field]) {
			t.Errorf("warning %+v, want suffix %q", w, wantWarn[w.Field])
		}
	}
	sh, ok := b.Must(opstask.OpShow, `{"id":"`+byTitle["Ship invites"]+`"}`).Value().Interface().(opstask.ShowOut)
	if !ok {
		t.Fatal("show result type")
	}
	notes := strings.Join(sh.Data.Notes, "\n")
	for f, w := range wantWarn {
		if !strings.Contains(notes, "import: "+w) {
			t.Errorf("no %s note %q in %q", f, w, notes)
		}
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

// TestImportByNameOnly (G-DECIDE-5): a hand-written file that names a
// mission without an id gets the one mission of that name; a name with no
// row drops with a note naming the reference as "(no id)".
func TestImportByNameOnly(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	solo := h.ID(h.Must("mission.add", `{"title":"Solo"}`))
	file := writeFile(t, "hand.json", `[{"title":"by name","mission_name":"Solo"},{"title":"unknown","mission_name":"Nope"}]`)
	res, err := runImport(h, `{"file":`+opstest.Quote(file)+`}`, true)
	if err != nil {
		t.Fatal(err)
	}
	got := importIDs(h, res)
	if len(got) != 2 {
		t.Fatalf("imported %d", len(got))
	}
	_, r := get(h, schema.TableTasks, got[0])
	if field(r, "mission_id") != solo {
		t.Errorf("mission %q, want %s", field(r, "mission_id"), solo)
	}
	io := importOut(t, res)
	if len(io.Warnings) != 1 || !strings.HasSuffix(io.Warnings[0].Message, `dropped mission_id (no id) (not in this repo; no mission named "Nope")`) {
		t.Errorf("warnings %+v", io.Warnings)
	}
}

// importOut is task.import's typed result.
func importOut(t *testing.T, res op.Result) opstask.ImportOut {
	t.Helper()
	io, ok := res.Value().Interface().(opstask.ImportOut)
	if !ok {
		t.Fatalf("result is %T", res.Value().Interface())
	}
	return io
}
