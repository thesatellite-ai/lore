//go:build userscen

package userscen

// Every verb of the tasks area run once the way a person or an agent
// chains them on one task, each with --json: the run succeeds, its
// document fits docs/reference/openapi.json for that operation, and the
// verbs a script may repeat (start, watch, tick, relate, heartbeat,
// accept, a template or view re-read) answer the same way twice instead of
// failing or duplicating. Runs in alice's clone of a two-person team (the
// coordination verbs need the remote's signals ref).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// campTasksStep runs one --json command in c, requires success and checks
// the document against operation opID; it returns the result.
func campTasksStep(t *testing.T, c *th.Clone, opID string, args ...string) th.Result {
	t.Helper()
	r := c.Lore(append(args, "--json")...)
	if r.Code != 0 {
		t.Fatalf("%s: %s", opID, r)
	}
	campTasksCheckShape(t, opID, r.Stdout)
	return r
}

// campTasksTwice runs a step twice: the second run must succeed too and
// fit the same schema (a repeat by a retrying script).
func campTasksTwice(t *testing.T, c *th.Clone, opID string, args ...string) th.Result {
	t.Helper()
	campTasksStep(t, c, opID, args...)
	return campTasksStep(t, c, opID, args...)
}

// TestCampaignTasks_TaskVerbs chains every task verb on one task tree.
func TestCampaignTasks_TaskVerbs(t *testing.T) {
	tm := newTasksTeam(t)
	c := tm.ca
	id := tasksID(t, campTasksStep(t, c, "task.add", "task", "add", "Invite flow", "--tasklist", tm.tasklist,
		"--body", "- [ ] backend\n- [x] design"))
	other := tasksID(t, campTasksStep(t, c, "task.add", "task", "add", "Landing page", "--tasklist", tm.tasklist))

	campTasksTwice(t, c, "task.start", "task", "start", id)
	if st := tasksShow(t, c, id).Status; st != "in_progress" {
		t.Fatalf("start: status %s", st)
	}
	campTasksTwice(t, c, "task.watch", "task", "watch", id)
	if w := tasksShow(t, c, id).Watchers; len(w) != 1 {
		t.Fatalf("watch twice: watchers %v, want one", w)
	}
	campTasksTwice(t, c, "task.unwatch", "task", "unwatch", id)
	campTasksStep(t, c, "task.note", "task", "note", id, "backend half done")

	// Checklist: import the body's lines, add, tick twice, untick, edit,
	// list, promote one to a sub-task, remove one.
	campTasksStep(t, c, "task.checklist.import", "task", "checklist", "import", id, "--from", "body", "--yes")
	campTasksStep(t, c, "task.checklist.add", "task", "checklist", "add", id, "write the test")
	campTasksTwice(t, c, "task.checklist.tick", "task", "checklist", "tick", id, "1")
	campTasksTwice(t, c, "task.checklist.untick", "task", "checklist", "untick", id, "1")
	campTasksStep(t, c, "task.checklist.edit", "task", "checklist", "edit", id, "3", "write the e2e test")
	campTasksStep(t, c, "task.checklist.list", "task", "checklist", "list", id).Says(t, "write the e2e test")
	campTasksStep(t, c, "task.checklist.promote", "task", "checklist", "promote", id, "3")
	campTasksStep(t, c, "task.checklist.rm", "task", "checklist", "rm", id, "2")
	campTasksStep(t, c, "task.checklist.list", "task", "checklist", "list", id).Never(t, "design")

	// Relations, twice each.
	campTasksTwice(t, c, "task.relate", "task", "relate", id, "--relates-to", other)
	campTasksTwice(t, c, "task.unrelate", "task", "unrelate", id, "--relates-to", other)

	// Split into two sub-tasks; a re-run with the same --key adds nothing.
	campTasksStep(t, c, "task.split", "task", "split", id, "API", "Email", "--dry-run")
	campTasksStep(t, c, "task.split", "task", "split", id, "API", "Email", "--key", "split-1")
	campTasksStep(t, c, "task.split", "task", "split", id, "API", "Email", "--key", "split-1")
	kids := 0
	for _, x := range tasksList(t, c, "--all") {
		if tasksShow(t, c, x).ParentID == id {
			kids++
		}
	}
	if kids != 3 { // two from the split, one promoted checklist item
		t.Fatalf("after split twice with one key: %d sub-tasks, want 3", kids)
	}
	campTasksStep(t, c, "task.tree.check", "task", "tree", "check")

	// Time: log, show, edit, rm, restore (twice: the second restore is a
	// clear no-op or refusal, never a second entry).
	campTasksStep(t, c, "task.time.log", "task", "time", "log", id, "45m", "--note", "pairing")
	tr := campTasksStep(t, c, "task.time.show", "task", "time", "show", id, "--entries")
	entry := strings.Fields(tr.Stdout[strings.Index(tr.Stdout, "tme_"):])[0]
	entry = strings.Trim(entry, `",`)
	campTasksStep(t, c, "task.time.edit", "task", "time", "edit", entry, "--note", "pairing with bob")
	campTasksStep(t, c, "task.time.rm", "task", "time", "rm", entry, "--yes")
	campTasksStep(t, c, "task.time.restore", "task", "time", "restore", entry)
	campTasksStep(t, c, "task.time.stop", "task", "time", "stop", id)

	campTasksStep(t, c, "task.history", "task", "history", id).Says(t, id)
	campTasksStep(t, c, "task.board", "task", "board", "--group-by", "status")
	campTasksStep(t, c, "task.export", "task", "export", "--format", "json")
	campTasksStep(t, c, "task.stale", "task", "stale", "--days", "1")
	campTasksStep(t, c, "task.search", "task", "search", "invite").Says(t, id)

	// Proposed / someday / deferred: the triage views and accept/decline.
	prop := tasksID(t, campTasksStep(t, c, "task.add", "task", "add", "Try a cache", "--commitment", "proposed"))
	campTasksStep(t, c, "task.triage", "task", "triage").Says(t, prop)
	campTasksTwice(t, c, "task.accept", "task", "accept", prop)
	prop2 := tasksID(t, campTasksStep(t, c, "task.add", "task", "add", "Rewrite in Rust", "--commitment", "proposed"))
	campTasksStep(t, c, "task.decline", "task", "decline", prop2, "--reason", "wont_do")
	some := tasksID(t, campTasksStep(t, c, "task.add", "task", "add", "Dark mode", "--commitment", "someday"))
	campTasksStep(t, c, "task.someday", "task", "someday").Says(t, some)
	later := tasksID(t, campTasksStep(t, c, "task.add", "task", "add", "Renew cert", "--defer-until", "2026-12-01"))
	campTasksStep(t, c, "task.deferred", "task", "deferred").Says(t, later)

	// Recurrence: show, edit, skip, stop (stop asks first).
	rec := tasksID(t, campTasksStep(t, c, "task.add", "task", "add", "Weekly report", "--recur", "every week"))
	campTasksStep(t, c, "task.recur.show", "task", "recur", "show", rec).Says(t, "week")
	campTasksStep(t, c, "task.recur.edit", "task", "recur", "edit", rec, "every", "2", "weeks")
	campTasksStep(t, c, "task.recur.skip", "task", "recur", "skip", rec)
	if e := campTasksErr(t, c.Lore("task", "recur", "stop", rec, "--json")); e.Error.Code != "E_NEEDS_CONFIRM" {
		t.Fatalf("recur stop without --yes: %+v", e.Error)
	}
	campTasksStep(t, c, "task.recur.stop", "task", "recur", "stop", rec, "--yes")

	// Done with the sub-tasks first, merged; a cancel of another task.
	for _, x := range tasksList(t, c, "--all") {
		if s := tasksShow(t, c, x); s.ParentID == id && s.Status != "done" {
			campTasksStep(t, c, "task.done", "task", "done", x, "--no-lesson")
		}
	}
	// One checklist item is still open: done refuses (E_OPEN_ITEMS, with
	// the way out), --tick-all closes it.
	if e := campTasksErr(t, c.Lore("task", "done", id, "--no-lesson", "--json")); e.Error.Code != "E_OPEN_ITEMS" || e.Error.Hint == "" {
		t.Fatalf("done with an open item: %+v", e.Error)
	}
	campTasksStep(t, c, "task.done", "task", "done", id, "--no-lesson", "--tick-all")
	campTasksStep(t, c, "task.merged", "task", "merged", id)
	campTasksStep(t, c, "task.cancel", "task", "cancel", other, "--yes")
	if s := tasksShow(t, c, other); s.Status != "cancelled" {
		t.Fatalf("cancel: %+v", s)
	}
}

// TestCampaignTasks_TemplatesAndViews: the template and view verbs, their
// reverse path (add → show → edit → rename → list → delete → restore) and
// the name rules (a name with spaces or slashes is refused with a hint).
func TestCampaignTasks_TemplatesAndViews(t *testing.T) {
	s := newTasksSolo(t)
	c := s.c
	yaml := "name: release_checklist\ndescription: cut a release\nargs:\n  version: {type: text, required: true}\ntask:\n  title: \"Release ${arg:version}\"\n  children:\n    - {title: \"Tag ${arg:version}\"}\n    - {title: Notes}\n"
	f := filepath.Join(c.Dir, "..", "release.yaml")
	if err := os.WriteFile(f, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	campTasksStep(t, c, "task.template.add", "task", "template", "add", "release_checklist", "--from-file", f)
	campTasksStep(t, c, "task.template.show", "task", "template", "show", "release_checklist").Says(t, "Release")
	campTasksStep(t, c, "task.template.list", "task", "template", "list").Says(t, "release_checklist")
	campTasksStep(t, c, "task.add", "task", "add", "--template", "release_checklist", "--arg", "version=1.2", "--dry-run")
	campTasksStep(t, c, "task.add", "task", "add", "--template", "release_checklist", "--arg", "version=1.2")
	edited := strings.Replace(yaml, "cut a release", "cut and ship a release", 1)
	if err := os.WriteFile(f, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	campTasksStep(t, c, "task.template.edit", "task", "template", "edit", "release_checklist", "--from-file", f)
	campTasksStep(t, c, "task.template.rename", "task", "template", "rename", "release_checklist", "ship_checklist")
	campTasksStep(t, c, "task.template.show", "task", "template", "show", "ship_checklist").Says(t, "cut and ship")
	if e := campTasksErr(t, c.Lore("task", "template", "show", "release_checklist", "--json")); e.Error.Code != "E_NOT_FOUND" || e.Error.Hint == "" {
		t.Errorf("the old name after rename: %+v", e.Error)
	}
	for _, bad := range []string{"has space", "a/b", "Ünïcode", "x" + strings.Repeat("y", 300)} {
		e := campTasksErr(t, c.Lore("task", "template", "rename", "ship_checklist", bad, "--json"))
		if e.Error.Hint == "" {
			t.Errorf("rename to %q: refused without a hint: %+v", bad, e.Error)
		}
	}
	if e := campTasksErr(t, c.Lore("task", "template", "delete", "ship_checklist", "--json")); e.Error.Code != "E_NEEDS_CONFIRM" {
		t.Fatalf("template delete without --yes: %+v", e.Error)
	}
	campTasksStep(t, c, "task.template.delete", "task", "template", "delete", "ship_checklist", "--yes")
	c.Lore("task", "template", "list").OK(t).Never(t, "ship_checklist")

	campTasksStep(t, c, "task.view.add", "task", "view", "add", "my_urgent", "--where", "priority>=high", "--sort", "urgency:desc")
	if e := campTasksErr(t, c.Lore("task", "view", "add", "my_urgent", "--where", "priority>=high", "--json")); e.Error.Hint == "" {
		t.Errorf("a second view of one name: refused without a hint: %+v", e.Error)
	}
	campTasksStep(t, c, "task.view.show", "task", "view", "show", "my_urgent").Says(t, "priority")
	campTasksStep(t, c, "task.view.edit", "task", "view", "edit", "my_urgent", "--sort", "due_at:asc")
	campTasksStep(t, c, "task.view.list", "task", "view", "list").Says(t, "my_urgent")
	tasksAdd(t, c, "an urgent thing", "--priority", "urgent")
	c.MustLore("task", "list", "--view", "my_urgent").Says(t, "an urgent thing")
	campTasksStep(t, c, "task.view.delete", "task", "view", "delete", "my_urgent", "--yes")
	c.Lore("task", "view", "list").OK(t).Never(t, "my_urgent")
}
