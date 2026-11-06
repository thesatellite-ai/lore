package task_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// runImport runs task.import straight from ImportOps (the composition root
// registers it beside Ops).
func runImport(h *opstest.H, input string, yes bool) (op.Result, error) {
	h.T.Helper()
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		h.T.Fatal(err)
	}
	e := opstask.ImportOps(h.App)[0]
	return h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(input)}, op.Caller{Actor: actor, Yes: yes})
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// importJSON is the "rows" of an import output.
func importIDs(h *opstest.H, res op.Result) []string {
	h.T.Helper()
	var out []string
	for _, part := range strings.Split(h.JSON(res), `"id":"`)[1:] {
		out = append(out, part[:strings.IndexByte(part, '"')])
	}
	return out
}

// XQ9 + PROVENANCE 5.1: a CSV import creates one task per row, each with
// _o.import {source: csv, ext_id}, all in one bat_ batch of kind import.
func TestImportCSV(t *testing.T) {
	h := opstest.New(t, repoClient(false)) // an own repo: a client repo drops _o.import without a src (PROVENANCE 8.1)
	f := writeFile(t, "tasks.csv", "id,title,priority,notes\nAGY-1,Fix login,high,x\nAGY-2,Add export,,y\n")
	in := `{"file":` + opstest.Quote(f) + `,"map":["notes=body"]}`
	res, err := runImport(h, in, false)
	if err != nil {
		t.Fatal(err)
	}
	got := importIDs(h, res)
	if len(got) != 2 || !strings.HasPrefix(got[0], "tsk_") {
		t.Fatalf("ids %v in %s", got, h.JSON(res))
	}
	_, r := get(h, schema.TableTasks, got[0])
	if field(r, "title") != "Fix login" || field(r, "priority") != "high" || field(r, "body") != "x" {
		t.Fatalf("row fields %v", r.Fields)
	}
	o := r.Origin.String()
	if !strings.Contains(o, `"import":{`) || !strings.Contains(o, `"source":"csv"`) || !strings.Contains(o, `"ext_id":"AGY-1"`) {
		t.Fatalf("_o = %s", o)
	}
	batch := h.Field(res, "batch")
	evs, err := h.App.BatchEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.Batch.String() == batch && ev.Table == schema.TableTasks {
			n++
			if ev.Kind != "import" {
				t.Fatalf("batch kind %q", ev.Kind)
			}
		}
	}
	if !strings.HasPrefix(batch, "bat_") || n != 2 {
		t.Fatalf("batch %q holds %d task events", batch, n)
	}
}

// A JSON import without an id column names ext ids <file>:<row>; a second
// import of the same file adds nothing (G-TASK-3, TSQ8 dedupe by external
// id: behaviour changed from "reported as duplicate_import"): every row is
// existing, names the first import's task, and no duplicate is left for
// PROV9 to report.
func TestImportJSONTwiceIsDuplicate(t *testing.T) {
	h := opstest.New(t, repoClient(false)) // an own repo: a client repo drops _o.import without a src (PROVENANCE 8.1)
	f := writeFile(t, "plan.json", `[{"title":"Write docs","priority":"low"},{"title":"Ship","due":"2026-12-01"}]`)
	in := `{"file":` + opstest.Quote(f) + `}`
	first, err := runImport(h, in, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runImport(h, in, false)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs, secondIDs := importIDs(h, first), importIDs(h, second)
	if len(firstIDs) != 2 || strings.Join(secondIDs, ",") != strings.Join(firstIDs, ",") ||
		strings.Count(h.JSON(second), `"existing":true`) != 2 {
		t.Fatalf("second import: %s (first %v)", h.JSON(second), firstIDs)
	}
	dups, err := opstask.FindDuplicateImports(context.Background(), h.App)
	if err != nil {
		t.Fatal(err)
	}
	if len(dups) != 0 {
		t.Fatalf("dups = %+v", dups)
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// XQ2: 25 rows over batch.preview_rows 20 ask first, listing 20; --yes
// writes them. A bad row writes nothing. --dry-run writes nothing.
func TestImportPreviewAndChecks(t *testing.T) {
	h := opstest.New(t, repoClient(false)) // an own repo: a client repo drops _o.import without a src (PROVENANCE 8.1)
	var b strings.Builder
	b.WriteString("title\n")
	for i := range 25 {
		b.WriteString("task " + strconv.Itoa(i) + "\n")
	}
	f := writeFile(t, "many.csv", b.String())
	in := `{"file":` + opstest.Quote(f) + `}`
	_, err := runImport(h, in, false)
	if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "25 rows") || !strings.Contains(err.Error(), "and 5 more") {
		t.Fatalf("want a preview, got %v", err)
	}
	if n := len(taskRows(h)); n != 0 {
		t.Fatalf("preview wrote %d tasks", n)
	}
	if _, err = runImport(h, `{"file":`+opstest.Quote(f)+`,"dry_run":true}`, false); err != nil {
		t.Fatal(err)
	}
	if _, err = runImport(h, in, true); err != nil {
		t.Fatal(err)
	}
	if n := len(taskRows(h)); n != 25 {
		t.Fatalf("--yes wrote %d tasks", n)
	}
	bad := writeFile(t, "bad.csv", "title,priority\nok,high\nbroken,huge\n")
	if _, err = runImport(h, `{"file":`+opstest.Quote(bad)+`}`, true); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bad priority: %v", err)
	}
	if n := len(taskRows(h)); n != 25 {
		t.Fatalf("a bad file wrote rows: %d", n)
	}
	for name, input := range map[string]string{
		"unknown column": `{"file":` + opstest.Quote(writeFile(t, "c.csv", "title,colour\na,red\n")) + `}`,
		"plugin source":  `{"file":` + opstest.Quote(f) + `,"from":"jira"}`,
		"bad map":        `{"file":` + opstest.Quote(f) + `,"map":["x=nope"]}`,
		"not an array":   `{"file":` + opstest.Quote(writeFile(t, "o.json", `{"title":"x"}`)) + `}`,
	} {
		if _, err = runImport(h, input, true); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func taskRows(h *opstest.H) []string {
	h.T.Helper()
	rows, err := h.App.List(context.Background(), "repo", schema.TableTasks)
	if err != nil {
		h.T.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if !r.Deleted {
			out = append(out, r.ID.String())
		}
	}
	return out
}
