package task_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestExportInternalRefusedInClientRepo (OV47): task export --internal
// adds the internal fields (time entries) in an internal repo and is
// refused in a client repo (and when the repo cannot be placed: fail
// closed); a plain export works in both and never carries them.
func TestExportInternalRefusedInClientRepo(t *testing.T) {
	t.Parallel()
	internal := opstest.New(t, repoClient(false))
	id := add(internal, "billable", "")
	internal.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"1h","at":"2026-11-02"}`)
	content := func(h *opstest.H, in string) string {
		t.Helper()
		var out opstask.ExportOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpExport, in))), &out); err != nil {
			t.Fatal(err)
		}
		return out.Content
	}
	if got := content(internal, `{"format":"json","internal":true}`); !strings.Contains(got, "time_entries") {
		t.Fatalf("internal export lacks the time entries: %s", got)
	}
	if got := content(internal, `{"format":"json"}`); strings.Contains(got, "time_entries") {
		t.Fatalf("plain export carries internal fields: %s", got)
	}
	for name, h := range map[string]*opstest.H{
		"client repo":      opstest.New(t, repoClient(true)),
		"unplaceable repo": opstest.New(t, repoClientErr()),
	} {
		add(h, "client task", "")
		if _, err := h.Run(opstask.OpExport, `{"format":"json","internal":true}`); !errs.Has(err, errs.InvalidInput) && !errs.Has(err, errs.StoreUnreachable) {
			t.Fatalf("%s: --internal export: %v", name, err)
		}
		content(h, `{"format":"json"}`)
	}
}

// TestImportReadsChildrenAndChecklist (OV47): an export with
// --with-children --with-checklist imports into another repo with the
// sub-task under its parent and every checklist item (text and done) on
// the task that had it.
func TestImportReadsChildrenAndChecklist(t *testing.T) {
	t.Parallel()
	a := opstest.New(t, repoClient(false))
	parent := add(a, "Parent", "")
	add(a, "Child", `"parent":"`+parent+`"`)
	a.Must(opstask.OpChecklistAdd, `{"id":"`+parent+`","text":"first","items":["second"]}`)
	a.Must(opstask.OpChecklistTick, `{"id":"`+parent+`","item":"1"}`)
	var out opstask.ExportOut
	if err := json.Unmarshal([]byte(a.JSON(a.Must(opstask.OpExport, `{"format":"json","with_children":true,"with_checklist":true}`))), &out); err != nil {
		t.Fatal(err)
	}
	b := opstest.New(t, repoClient(false))
	res, err := runImport(b, `{"file":`+opstest.Quote(writeFile(t, "tasks.json", out.Content))+`}`, true)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out.Content)
	}
	byTitle := map[string]string{}
	for _, id := range importIDs(b, res) {
		_, r := get(b, schema.TableTasks, id)
		byTitle[field(r, "title")] = id
	}
	if len(byTitle) != 2 {
		t.Fatalf("imported %v", byTitle)
	}
	_, c := get(b, schema.TableTasks, byTitle["Child"])
	if field(c, "parent_id") != byTitle["Parent"] {
		t.Fatalf("child parent %q, want %q", field(c, "parent_id"), byTitle["Parent"])
	}
	_, p := get(b, schema.TableTasks, byTitle["Parent"])
	items := field(p, schema.FieldTaskChecklist)
	if !strings.Contains(items, `"first"`) || !strings.Contains(items, `"second"`) || strings.Count(items, `"done":true`) != 1 {
		t.Fatalf("checklist %s", items)
	}
}
