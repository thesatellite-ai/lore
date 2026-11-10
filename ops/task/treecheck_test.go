package task_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// setParent writes parent_id into the task's row file, as a merge does: no
// engine rule runs (the engine refuses a ref to a missing or trashed row).
func setParent(h *opstest.H, id, parent string) {
	h.T.Helper()
	root, err := os.OpenRoot(filepath.Join(h.Repo, ".lore", "data", schema.TableTasks))
	if err != nil {
		h.T.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	b, err := root.ReadFile(id + ".json")
	if err != nil {
		h.T.Fatal(err)
	}
	doc := parentRe.ReplaceAllString(string(b), "")
	doc = strings.Replace(doc, "{", `{"`+schema.FieldTaskParent+`":"`+parent+`",`, 1)
	if err := root.WriteFile(id+".json", []byte(doc), 0o600); err != nil {
		h.T.Fatal(err)
	}
}

// parentRe matches a stored parent_id member.
var parentRe = regexp.MustCompile(`"` + schema.FieldTaskParent + `":\s*"[^"]*",?`)

// TestTreeCheck: a clean tree has no finding; a merged cycle, a missing
// parent, a trashed parent, an open child of a done parent and shared
// ranks are each reported with their fix; --fix writes only the cycle's
// shown result, and a second check finds the cycle gone (6.13).
func TestTreeCheck(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	p := add(h, "parent", "")
	add(h, "child", `"parent":"`+p+`"`)
	if out := h.Must(opstask.OpTreeCheck, `{}`); !strings.Contains(opstest.Text(out), "task tree ok (2 tasks)") {
		t.Fatalf("clean tree: %s", opstest.Text(out))
	}
	x, y := add(h, "x", ""), add(h, "y", "")
	setParent(h, x, y)
	setParent(h, y, x)
	orphan := add(h, "orphan", "")
	gone := add(h, "gone", "")
	gid, _ := ids.ParseAny(gone)
	if _, err := h.App.Delete(context.Background(), engine.Ref{Table: schema.TableTasks, ID: gid}); err != nil {
		t.Fatal(err)
	}
	setParent(h, orphan, gone) // a merge: the other side's child of a now-trashed parent
	lost := add(h, "lost", "")
	setParent(h, lost, "tsk_"+strings.Repeat("0", 31)+"1")
	dp := add(h, "done parent", "")
	h.Must(opstask.OpDone, `{"id":"`+dp+`"}`)
	oc := add(h, "open child", "")
	setParent(h, oc, dp)
	out := h.Must(opstask.OpTreeCheck, `{}`)
	js := h.JSON(out)
	for _, want := range []string{`"kind":"parent_cycle"`, `"code":"E_PARENT_CYCLE"`, `"id":"` + x + `"`, "in the trash", "lore trash restore " + gone,
		"is missing", "--no-parent", `"kind":"parent_closed"`, `"id":"` + oc + `"`} {
		if !strings.Contains(js, want) {
			t.Errorf("findings lack %q: %s", want, js)
		}
	}
	fixed := h.Must(opstask.OpTreeCheck, `{"fix":true}`)
	if !strings.Contains(h.JSON(fixed), `"fixed":1}`) {
		t.Fatalf("fixed = %s", h.JSON(fixed))
	}
	_, xr := get(h, schema.TableTasks, x)
	if field(xr, schema.FieldTaskParent) != "" {
		t.Fatalf("cycle top still has parent %s", field(xr, schema.FieldTaskParent))
	}
	if again := h.JSON(h.Must(opstask.OpTreeCheck, `{}`)); strings.Contains(again, "parent_cycle") || !strings.Contains(again, `"kind":"orphan"`) {
		t.Fatalf("after --fix: %s", again)
	}
}

// TestTreeCheckWrongNoun: `task tree check --project` given an id of
// another kind (a task id) is E_INVALID_ID naming the project noun
// ("… is not a project id"), not the task noun of the command.
func TestTreeCheckWrongNoun(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	tid := add(h, "a task", "")
	msg := h.Fails(opstask.OpTreeCheck, `{"project":"`+tid+`"}`, errs.InvalidID)
	if !strings.Contains(msg, tid+`" is not a project id (prj_…)`) || strings.Contains(msg, "is not a task id") {
		t.Errorf("message does not name the project noun: %q", msg)
	}
}
