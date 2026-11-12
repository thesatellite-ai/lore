package row_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/row"
)

// decode reads an operation's JSON result into v.
func decode[T any](t *testing.T, h *opstest.H, name, in string) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(h.JSON(h.Must(name, in))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestPathPrintsTheRowFile: row.path answers the absolute path of the row
// file the backend holds (it exists and is the row), with the store; the
// text form is the path alone.
func TestPathPrintsTheRowFile(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := h.ID(h.MustYes("memory.add", `{"body":"retry the deploy twice"}`))
	out := decode[row.PathOut](t, h, row.OpPath, `{"id":"`+id+`"}`)
	if out.ID != id || out.Table != "memories" || out.Store != "repo" || !filepath.IsAbs(out.Path) || filepath.Base(out.Path) != id+".json" {
		t.Fatalf("row.path = %+v", out)
	}
	b, err := os.ReadFile(out.Path)
	if err != nil || !strings.Contains(string(b), "retry the deploy twice") {
		t.Fatalf("the printed file is not the row (%v): %s", err, b)
	}
	if text := opstest.Text(h.Must(row.OpPath, `{"id":"`+id+`"}`)); text != out.Path+"\n" {
		t.Errorf("text form %q, want the path alone", text)
	}
}

// TestPathRefusals: a bad id and an id no attached store holds are refused
// with the next step; not found names where lore looked.
func TestPathRefusals(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.Fails(row.OpPath, `{"id":"not-an-id"}`, errs.InvalidID)
	h.Fails(row.OpPath, `{"id":"mem_01a1cf02b365751bac5520b3d393eef1"}`, errs.NotFound)
	_, err := h.Run(row.OpPath, `{"id":"mem_01a1cf02b365751bac5520b3d393eef1"}`)
	body := errs.EnvelopeOf(err).Error
	for _, want := range []string{"looked in: repo (", filepath.Join(".lore", "data") + ")", "lore search"} {
		if !strings.Contains(body.Message+" "+body.Hint, want) {
			t.Errorf("not found lacks %q: %+v", want, body)
		}
	}
}

// TestEditWritesThroughTheWritePath: a changed view writes the changed
// fields only (new HLC, the row file changes); the same view again is no
// change and writes nothing; a refused view writes nothing.
func TestEditWritesThroughTheWritePath(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := h.ID(h.MustYes("task.add", `{"title":"ship the fix","body":"first"}`))
	view := decode[row.MarkdownOut](t, h, row.OpMarkdown, `{"id":"`+id+`"}`)
	if view.BodyField != "body" || !strings.HasPrefix(view.Markdown, "---\n") || !strings.Contains(view.Markdown, "title: ship the fix") {
		t.Fatalf("view = %+v", view)
	}
	edited := strings.Replace(view.Markdown, "title: ship the fix", "title: 'ship it: today # now'", 1) + "second line\n"
	in, _ := json.Marshal(row.EditIn{ID: id, Markdown: edited})
	out := decode[row.EditOut](t, h, row.OpEdit, string(in))
	if out.NoChange || strings.Join(out.Changed, ",") != "body,title" || out.Store != "repo" {
		t.Fatalf("row.edit = %+v, want body and title written", out)
	}
	shown := showJSON(t, h, `{"id":"`+id+`"}`)
	if shown.Fields["title"] != "ship it: today # now" || shown.Fields["body"] != "first\nsecond line" {
		t.Fatalf("after the edit: %+v", shown.Fields)
	}
	// the same (now stale) view again: its _hlc is older than the row's
	h.Fails(row.OpEdit, string(in), errs.Stale)
	// a fresh view written back unchanged: no change, the file untouched
	fresh := decode[row.MarkdownOut](t, h, row.OpMarkdown, `{"id":"`+id+`"}`)
	path := decode[row.PathOut](t, h, row.OpPath, `{"id":"`+id+`"}`).Path
	before, _ := os.ReadFile(path)
	file := filepath.Join(t.TempDir(), "view.md")
	if err := os.WriteFile(file, []byte(fresh.Markdown), 0o600); err != nil {
		t.Fatal(err)
	}
	again := decode[row.EditOut](t, h, row.OpEdit, `{"id":"`+id+`","file":"`+file+`"}`)
	if !again.NoChange || len(again.Changed) != 0 {
		t.Fatalf("unchanged view = %+v, want no change", again)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("a no-change edit rewrote the row file")
	}
	if !strings.Contains(opstest.Text(h.Must(row.OpEdit, `{"id":"`+id+`","file":"`+file+`"}`)), "no change") {
		t.Errorf("the no-change text does not say so")
	}
	// a bad value is refused with file:line and nothing is written
	bad := strings.Replace(fresh.Markdown, "status: todo", "status: someday-maybe", 1)
	if err := os.WriteFile(file, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := h.Fails(row.OpEdit, `{"id":"`+id+`","file":"`+file+`"}`, errs.InvalidInput)
	if !strings.Contains(msg, file+":") || !strings.Contains(msg, "status") {
		t.Errorf("refusal names no file:line: %s", msg)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("a refused edit changed the row file")
	}
}

// TestEditInputRefusals: no view, both inputs, an unreadable file.
func TestEditInputRefusals(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := h.ID(h.MustYes("memory.add", `{"body":"a memory"}`))
	h.Fails(row.OpEdit, `{"id":"`+id+`"}`, errs.InvalidInput)
	h.Fails(row.OpEdit, `{"id":"`+id+`","file":"x.md","markdown":"---\n---\n"}`, errs.InvalidInput)
	h.Fails(row.OpEdit, `{"id":"`+id+`","file":"`+filepath.Join(t.TempDir(), "missing.md")+`"}`, errs.InvalidInput)
}
