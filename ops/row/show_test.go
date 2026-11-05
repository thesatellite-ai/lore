package row_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/row"
)

// showJSON decodes a row.show result.
func showJSON(t *testing.T, h *opstest.H, in string) row.ShowOut {
	t.Helper()
	var out row.ShowOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(row.OpShow, in))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestShowAnyTable (later 442): `lore show <id>` reads a row of any table
// from its id prefix (memory, task, var), in the store that holds it, with
// its fields in text and --json; the id may carry edge whitespace; the
// human first line is id, table, store and the title (or name).
func TestShowAnyTable(t *testing.T) {
	h := opstest.New(t)
	cases := []struct {
		name, add, input, table, title string
		field, want                    string
	}{
		{"memory", "memory.add", `{"body":"retry the deploy"}`, "memories", "", "body", "retry the deploy"},
		{"task", "task.add", `{"title":"ship the fix"}`, "tasks", "ship the fix", "title", "ship the fix"},
		{"var", "vars.set", `{"name":"api_host","value":"api.example.test"}`, "vars", "api_host", "value", "api.example.test"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := h.ID(h.MustYes(c.add, c.input))
			for _, in := range []string{id, "  " + id + "\t"} {
				out := showJSON(t, h, `{"id":`+opstest.Quote(in)+`}`)
				if out.ID != id || out.Table != c.table || out.Store != "repo" || out.Fields[c.field] != c.want {
					t.Fatalf("show %q: %+v", in, out)
				}
				if out.Origin.Recorded || len(out.Audit.CreatedAt) != 0 {
					t.Fatalf("show without --origin carries origin blocks: %+v", out)
				}
			}
			text := opstest.Text(h.Must(row.OpShow, `{"id":"`+id+`"}`))
			first := strings.SplitN(text, "\n", 2)[0]
			wantFirst := id + "  " + c.table + "  repo"
			if c.title != "" {
				wantFirst += `  "` + c.title + `"`
			}
			if first != wantFirst || !strings.Contains(text, "  "+c.field+": "+c.want+"\n") {
				t.Fatalf("text:\n%s\nwant first line %q", text, wantFirst)
			}
		})
	}
}

// TestShowOrigin: --origin adds the recorded origin (store, command), the
// audit fields with the writer and an empty move list, in text and JSON.
func TestShowOrigin(t *testing.T) {
	h := opstest.New(t)
	id := h.ID(h.Must("memory.add", `{"body":"with origin"}`))
	out := showJSON(t, h, `{"id":"`+id+`","origin":true}`)
	if !out.Origin.Recorded || out.Origin.Store != "repo" || out.Audit.CreatedAt == "" || out.Audit.CreatedBy.ID == "" ||
		out.Lineage.Moves == nil || len(out.Lineage.Moves) != 0 {
		t.Fatalf("origin: %+v", out)
	}
	text := opstest.Text(h.Must(row.OpShow, `{"id":"`+id+`","origin":true}`))
	for _, want := range []string{"origin (written once)", "who and when", "lineage\n  no moves", "agent session"} {
		if !strings.Contains(text, want) {
			t.Errorf("origin text has no %q:\n%s", want, text)
		}
	}
}

// TestShowErrors: the answer per id shape, and --force without
// --reveal; a trashed row says it is in the trash.
func TestShowErrors(t *testing.T) {
	h := opstest.New(t)
	cases := []struct {
		name, input string
		code        errs.Code
	}{
		// a value that is not an id is "not found" (lookup.BadID, as every
		// lookup answers it)
		{"not an id", `{"id":"nope"}`, errs.NotFound},
		{"empty", `{"id":"   "}`, errs.NotFound},
		{"unknown memory", `{"id":"mem_01a1a5aca7f57659be7320c5f4b7f00d"}`, errs.NotFound},
		{"force without reveal", `{"id":"` + h.ID(h.Must("memory.add", `{"body":"f"}`)) + `","force":true}`, errs.InvalidInput},
	}
	for _, c := range cases {
		h.Fails(row.OpShow, c.input, c.code)
	}
	gone := h.ID(h.Must("memory.add", `{"body":"to the trash"}`))
	h.MustYes("memory.delete", `{"id":"`+gone+`"}`)
	h.Fails(row.OpShow, `{"id":"`+gone+`"}`, errs.InTrash)
}
