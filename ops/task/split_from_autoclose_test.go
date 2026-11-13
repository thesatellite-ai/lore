package task_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestSplitFromFile (TASK 6.3, OV2): task split --from reads a YAML list
// (plain titles and {title, priority, …} pieces mixed) or, when the text
// is not a YAML list, one title per non-empty line; the sub-tasks land in
// file order with each piece's own priority, the shared one filling the
// rest. A YAML list that does not parse is E_INVALID_INPUT with nothing
// added.
func TestSplitFromFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	type piece struct{ title, priority string }
	cases := []struct {
		name, file string
		want       []piece
	}{
		{"yaml list of titles and pieces", write("split.yaml", "- first\n- title: second\n  priority: urgent\n- title: third\n"),
			[]piece{{"first", "low"}, {"second", "urgent"}, {"third", "low"}}},
		{"plain lines, blanks skipped", write("split.txt", "alpha\n\n  beta  \n"),
			[]piece{{"alpha", "low"}, {"beta", "low"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t, repoClient(false))
			parent := add(h, "parent", "")
			res := h.Must(opstask.OpSplit, `{"id":"`+parent+`","priority":"low","from":`+opstest.Quote(c.file)+`}`)
			var out struct {
				Added []struct {
					ID string `json:"id"`
				} `json:"added"`
			}
			if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil || len(out.Added) != len(c.want) {
				t.Fatalf("split --from added %s (%v), want %d sub-tasks", h.JSON(res), err, len(c.want))
			}
			for i, w := range c.want {
				_, r := get(h, schema.TableTasks, out.Added[i].ID)
				if got := field(r, "title"); got != w.title {
					t.Fatalf("sub-task %d title %q, want %q (file order)", i, got, w.title)
				}
				if got := field(r, "priority"); got != w.priority {
					t.Fatalf("sub-task %q priority %q, want %q", w.title, got, w.priority)
				}
				if got := field(r, "parent_id"); got != parent {
					t.Fatalf("sub-task %q parent %q, want %q", w.title, got, parent)
				}
			}
		})
	}
	t.Run("broken yaml list refused", func(t *testing.T) {
		t.Parallel()
		h := opstest.New(t, repoClient(false))
		parent := add(h, "parent", "")
		bad := write("bad.yaml", "- title: [unclosed\n")
		h.Fails(opstask.OpSplit, `{"id":"`+parent+`","from":`+opstest.Quote(bad)+`}`, errs.InvalidInput)
		if got := childOrder(t, h, parent); got != "" {
			t.Fatalf("a refused split added %q", got)
		}
	})
}

// TestParentAutocloseModes (TASK 6.7, TSQ2, OV2): when the last open
// sub-task closes, tasks.parent_autoclose decides what happens to the
// parent: off leaves it open and says nothing, suggest (the default)
// leaves it open and answers the close command as next, on closes the
// parent in the same write and reports it. A parent that still has an
// open sub-task is never touched.
func TestParentAutocloseModes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		mode       string // "" = the default
		wantStatus string
		wantNext   bool
		wantClosed bool
	}{
		{"", schema.TaskStatusTodo, true, false},
		{"off", schema.TaskStatusTodo, false, false},
		{"suggest", schema.TaskStatusTodo, true, false},
		{"on", schema.TaskStatusDone, false, true},
	} {
		name := c.mode
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t, repoClient(false))
			if c.mode != "" {
				h.MustYes(settings.OpPolicySet, `{"key":"tasks.parent_autoclose","value":"`+c.mode+`"}`)
			}
			p := add(h, "parent", "")
			c1 := add(h, "one", `"parent":"`+p+`"`)
			c2 := add(h, "two", `"parent":"`+p+`"`)

			first := doneOut(t, h, c1)
			if first.Parent.Closed || first.Next.Command != "" {
				t.Fatalf("a sibling is still open: the parent must be left alone, got %+v", first)
			}
			if _, r := get(h, schema.TableTasks, p); field(r, "status") != schema.TaskStatusTodo {
				t.Fatalf("parent closed while a sub-task is open: %s", field(r, "status"))
			}

			last := doneOut(t, h, c2)
			if _, r := get(h, schema.TableTasks, p); field(r, "status") != c.wantStatus {
				t.Fatalf("parent status %q, want %q", field(r, "status"), c.wantStatus)
			}
			if last.Parent.Closed != c.wantClosed {
				t.Fatalf("parent.closed = %v, want %v", last.Parent.Closed, c.wantClosed)
			}
			if gotNext := last.Next.Command != ""; gotNext != c.wantNext {
				t.Fatalf("next = %+v, want a close suggestion: %v", last.Next, c.wantNext)
			}
			if c.wantNext && (len(last.Next.Args) != 3 || last.Next.Args[1] != "done" || last.Next.Args[2] != p) {
				t.Fatalf("the suggestion must be lore task done --id %s, got %v", p, last.Next.Args)
			}
		})
	}
}

// doneOut closes id (no lesson) and returns the typed answer.
func doneOut(t *testing.T, h *opstest.H, id string) opstask.DoneOut {
	t.Helper()
	out, ok := h.Must(opstask.OpDone, `{"id":"`+id+`","no_lesson":true}`).Value().Interface().(opstask.DoneOut)
	if !ok {
		t.Fatal("done result type")
	}
	return out
}
