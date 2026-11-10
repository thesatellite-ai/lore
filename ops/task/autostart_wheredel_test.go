package task_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestAutoStartParentMatrix (FA-TASK-2, TASK 6.7): with
// tasks.auto_start_parent on, a child started by task start or by task
// edit --status in_progress starts its todo parent, with no time span on
// the parent (the work is on the child) and a line saying so; off (the
// default) leaves the parent todo; a parent already done, blocked or in
// progress is left alone; an edit that moves the child under a new parent
// starts that new parent.
func TestAutoStartParentMatrix(t *testing.T) {
	t.Parallel()
	type tc struct {
		name, policy, parentStatus, how string
		started                         bool
	}
	cases := []tc{
		{"on start todo", "true", schema.TaskStatusTodo, "start", true},
		{"on edit todo", "true", schema.TaskStatusTodo, "edit", true},
		{"off start todo", "", schema.TaskStatusTodo, "start", false},
		{"explicit off edit todo", "false", schema.TaskStatusTodo, "edit", false},
		{"on start blocked parent", "true", schema.TaskStatusBlocked, "start", false},
		{"on edit in-progress parent", "true", schema.TaskStatusInProgress, "edit", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t, repoClient(false)) // time spans live in internal stores only (PC-LEAK-1)
			if c.policy != "" {
				h.MustYes(settings.OpPolicySet, `{"key":"tasks.auto_start_parent","value":"`+c.policy+`"}`)
			}
			p := add(h, "parent", "")
			if c.parentStatus != schema.TaskStatusTodo {
				h.Must(opstask.OpEdit, `{"id":"`+p+`","status":"`+c.parentStatus+`"}`)
			}
			_, before := get(h, schema.TableTasks, p)
			child := add(h, "child", `"parent":"`+p+`"`)
			var got string
			if c.how == "start" {
				got = opstest.Text(h.Must(opstask.OpStart, `{"id":"`+child+`"}`))
			} else {
				got = opstest.Text(h.Must(opstask.OpEdit, `{"id":"`+child+`","status":"in_progress"}`))
			}
			_, pr := get(h, schema.TableTasks, p)
			if c.started {
				if field(pr, "status") != schema.TaskStatusInProgress || !strings.Contains(got, "started too (tasks.auto_start_parent: on)") {
					t.Fatalf("parent not started: %s / %q", field(pr, "status"), got)
				}
				if n := len(opstask.TimeOpen(pr)); n != 0 {
					t.Fatalf("auto start opened %d time spans on the parent", n)
				}
				if field(pr, "started_at") == "" {
					t.Fatal("auto start left started_at empty")
				}
			} else if field(pr, "status") != field(before, "status") || strings.Contains(got, "started too") {
				t.Fatalf("parent changed: %s -> %s / %q", field(before, "status"), field(pr, "status"), got)
			}
			if _, cr := get(h, schema.TableTasks, child); field(cr, "status") != schema.TaskStatusInProgress {
				t.Fatalf("child status %s", field(cr, "status"))
			}
		})
	}
	t.Run("edit moves under a new todo parent", func(t *testing.T) {
		t.Parallel()
		h := opstest.New(t, repoClient(false))
		h.MustYes(settings.OpPolicySet, `{"key":"tasks.auto_start_parent","value":"true"}`)
		old := add(h, "old parent", "")
		next := add(h, "new parent", "")
		child := add(h, "child", `"parent":"`+old+`"`)
		h.Must(opstask.OpEdit, `{"id":"`+child+`","parent":"`+next+`","status":"in_progress"}`)
		if _, r := get(h, schema.TableTasks, next); field(r, "status") != schema.TaskStatusInProgress {
			t.Fatalf("new parent %s", field(r, "status"))
		}
		if _, r := get(h, schema.TableTasks, old); field(r, "status") != schema.TaskStatusTodo {
			t.Fatalf("old parent %s", field(r, "status"))
		}
	})
}

// TestDeleteWhere (FA-TASK-8, DELETION 2.8): task delete --where
// --dry-run lists and writes nothing; without --yes it always asks, even
// for one match; a parent and its child both matching are counted once
// (the matching child is folded into its parent: 2 matched, 2 under them); every match and every sub-task go
// in one batch that one restore brings back; no match deletes nothing.
func TestDeleteWhere(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	p := add(h, "bulk parent", `"priority":"low"`)
	c := add(h, "bulk child", `"parent":"`+p+`","priority":"low"`)
	g := add(h, "bulk grandchild", `"parent":"`+c+`","priority":"high"`)
	solo := add(h, "bulk solo", `"priority":"low"`)
	keep := add(h, "keep", `"priority":"medium"`)
	all := []string{p, c, g, solo}

	before := listIDs(t, h, context.Background(), `{"all":true}`)
	dry := opstest.Text(h.Must(opstask.OpDelete, `{"where":["priority=low"],"dry_run":true}`))
	if !strings.Contains(dry, "would delete 4 task(s): 2 matched and 2 sub-task(s) under them") || !strings.Contains(dry, "nothing written") {
		t.Fatalf("dry run: %q", dry)
	}
	if got := listIDs(t, h, context.Background(), `{"all":true}`); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatal("--dry-run wrote")
	}
	h.Fails(opstask.OpDelete, `{"where":["priority=medium"]}`, errs.NeedsConfirm) // one match still asks
	h.Must(opstask.OpShow, `{"id":"`+keep+`"}`)
	if got := opstest.Text(h.MustYes(opstask.OpDelete, `{"where":["title=nosuchtitle"]}`)); !strings.Contains(got, "nothing deleted") {
		t.Fatalf("no match: %q", got)
	}

	got := opstest.Text(h.MustYes(opstask.OpDelete, `{"where":["priority=low"]}`))
	m := regexp.MustCompile(`4 tasks \(--where\) moved to the trash \(batch (bat_[0-9a-f]{32});`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("bulk delete line: %q", got)
	}
	for _, id := range all {
		h.Fails(opstask.OpShow, `{"id":"`+id+`"}`, errs.InTrash)
	}
	h.Must(opstask.OpShow, `{"id":"`+keep+`"}`)
	h.Must("trash.restore", `{"target":"`+m[1]+`"}`)
	for _, id := range all {
		h.Must(opstask.OpShow, `{"id":"`+id+`"}`)
	}
}
