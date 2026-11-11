package task_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// releaseYAML is TASK 11.2's example template.
const releaseYAML = `name: release
description: Cut and ship one release of a repo
args:
  version: {type: text, required: true}
  count: {type: number, default: "1"}
task:
  title: "Release ${arg:version}"
  priority: high
  checklist:
    - "changelog has ${arg:version}"
    - "tag pushed"
  children:
    - {title: "Bump version to ${arg:version}", estimate: 1}
    - {title: "Run full test suite", estimate: 1, after: [0]}
    - {title: "Deploy to staging", gate: required, after: [1]}
`

// TestTemplates: add from YAML, list/show, apply with args (dry run,
// missing / unknown / typed args, idempotent --key), rename, snapshot
// --from round trip, validation refusals, delete (TASK 11).
func TestTemplates(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	file := write("release.yaml", releaseYAML)
	h.Must(opstask.OpTemplateAdd, `{"name":"release","from_file":`+opstest.Quote(file)+`}`)
	h.Fails(opstask.OpTemplateAdd, `{"name":"release","from_file":`+opstest.Quote(file)+`}`, errs.Template)
	h.Fails(opstask.OpTemplateAdd, `{"name":"Bad-Name","from_file":`+opstest.Quote(file)+`}`, errs.Template)
	h.Fails(opstask.OpTemplateAdd, `{"name":"x","from_file":`+opstest.Quote(write("bad1.yaml", "task: {title: \"${arg:nope}\"}\n"))+`}`, errs.Template)
	h.Fails(opstask.OpTemplateAdd, `{"name":"x","from_file":`+opstest.Quote(write("bad2.yaml", "task: {title: a, children: [{title: b, after: [0]}]}\n"))+`}`, errs.Template)
	h.Fails(opstask.OpTemplateAdd, `{"name":"x","from_file":`+opstest.Quote(write("bad3.yaml", "task: {title: a, colour: red}\n"))+`}`, errs.Template)
	if got := opstest.Text(h.Must(opstask.OpTemplateList, `{}`)); !strings.Contains(got, "release") || !strings.Contains(got, "3 sub-tasks, 2 checklist items") {
		t.Fatalf("list: %s", got)
	}
	if got := opstest.Text(h.Must(opstask.OpTemplateShow, `{"name":"release"}`)); !strings.Contains(got, "Release ${arg:version}") {
		t.Fatalf("show: %s", got)
	}
	h.Fails(opstask.OpAdd, `{"template":"release"}`, errs.Template)
	h.Fails(opstask.OpAdd, `{"template":"release","args":["version=1","colour=red"]}`, errs.Template)
	h.Fails(opstask.OpAdd, `{"template":"release","args":["version=1","count=many"]}`, errs.Template)
	dry := opstest.Text(h.Must(opstask.OpAdd, `{"template":"release","args":["version=0.4.2"],"dry_run":true}`))
	if !strings.Contains(dry, "would add 4 tasks") || !strings.Contains(dry, "  Bump version to 0.4.2") {
		t.Fatalf("dry run: %s", dry)
	}
	out := opstest.Text(h.Must(opstask.OpAdd, `{"template":"release","args":["version=0.4.2"],"key":"release/0.4.2"}`))
	if !strings.Contains(out, "Release 0.4.2") || !strings.Contains(out, "added 1 task and 3 sub-tasks") {
		t.Fatalf("apply: %s", out)
	}
	root := strings.Fields(strings.TrimPrefix(out, "✓ "))[0]
	if again := opstest.Text(h.Must(opstask.OpAdd, `{"template":"release","args":["version=0.4.2"],"key":"release/0.4.2"}`)); !strings.Contains(again, "nothing added") {
		t.Fatalf("re-apply: %s", again)
	}
	kids := h.JSON(h.Must(opstask.OpList, `{"parent":"`+root+`"}`))
	if strings.Count(kids, `"parent_id":"`+root+`"`) != 3 {
		t.Fatalf("children: %s", kids)
	}
	if cl := opstest.Text(h.Must(opstask.OpChecklistList, `{"id":"`+root+`"}`)); !strings.Contains(cl, "changelog has 0.4.2") {
		t.Fatalf("checklist: %s", cl)
	}
	h.Must(opstask.OpTemplateRename, `{"name":"release","new_name":"ship_release"}`)
	h.Fails(opstask.OpTemplateShow, `{"name":"release"}`, errs.NotFound)
	h.Must(opstask.OpTemplateAdd, `{"name":"snap","from":`+opstest.Quote(root)+`}`)
	snap := opstest.Text(h.Must(opstask.OpTemplateShow, `{"name":"snap"}`))
	if !strings.Contains(snap, "Deploy to staging") || !strings.Contains(snap, "gate: required") || !strings.Contains(snap, "changelog has 0.4.2") {
		t.Fatalf("snapshot: %s", snap)
	}
	h.Fails(opstask.OpTemplateDelete, `{"name":"snap"}`, errs.NeedsConfirm)
	h.MustYes(opstask.OpTemplateDelete, `{"name":"snap"}`)
	h.Fails(opstask.OpTemplateShow, `{"name":"snap"}`, errs.NotFound)
}
