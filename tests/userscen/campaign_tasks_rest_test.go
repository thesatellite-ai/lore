//go:build userscen

package userscen

// CLI ↔ REST parity for the tasks area: the same read gives the same
// document over `lore <cmd> --json` and the running `lore api`, for every
// read operation of the area (lists, shows, searches, boards, triage
// views, time, checklist, recurrence, templates, views, sessions, inbox,
// mission status, tree check, export), and the same bad input gives the
// same error code, message and exit on both, with the HTTP status of its
// class: a typo is E_INVALID_ID before any confirmation (a delete over
// REST without "yes" too), a missing row E_NOT_FOUND.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCampaignTasks_RESTParity compares every read and a refusal matrix.
func TestCampaignTasks_RESTParity(t *testing.T) {
	s := newTasksSolo(t)
	c := s.c
	tl := tasksID(t, c.MustLore("tasklist", "add", "--title", "Sprint", "--body", "b", "--json"))
	m := tasksID(t, c.MustLore("mission", "add", "Invite flow", "--json"))
	p := tasksID(t, c.MustLore("plan", "add", "--title", "Rollout", "--body", "b", "--json"))
	w := tasksID(t, c.MustLore("workflow", "add", "--title", "Release", "--body", "steps", "--json"))
	tasksID(t, c.MustLore("reminder", "add", "renew cert", "--due", "2026-12-01", "--json"))
	id := tasksAdd(t, c, "Invite API", "--tasklist", tl, "--mission", m, "--body", "- [ ] one")
	c.MustLore("task", "checklist", "add", id, "two")
	c.MustLore("task", "time", "log", id, "30m")
	rec := tasksAdd(t, c, "Weekly report", "--recur", "every week")
	tasksAdd(t, c, "Try a cache", "--commitment", "proposed")
	f := filepath.Join(c.Dir, "..", "tpl.yaml")
	if err := os.WriteFile(f, []byte("name: ship\ntask:\n  title: Ship\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.MustLore("task", "template", "add", "ship", "--from-file", f)
	c.MustLore("task", "view", "add", "urgent", "--where", "priority>=high")
	srv := restStart(t, c)

	reads := []struct {
		cli  []string
		req  restReq
		opID string
	}{
		{[]string{"task", "list", "--all"}, restReq{method: http.MethodGet, path: "/v1/tasks?all=true"}, "task.list"},
		{[]string{"task", "show", id}, restReq{method: http.MethodGet, path: "/v1/tasks/" + id}, "task.show"},
		{[]string{"tasklist", "list"}, restReq{method: http.MethodGet, path: "/v1/tasklists"}, "tasklist.list"},
		{[]string{"tasklist", "show", tl}, restReq{method: http.MethodGet, path: "/v1/tasklists/" + tl}, "tasklist.show"},
		{[]string{"mission", "list"}, restReq{method: http.MethodGet, path: "/v1/missions"}, "mission.list"},
		{[]string{"mission", "show", m}, restReq{method: http.MethodGet, path: "/v1/missions/" + m}, "mission.show"},
		{[]string{"mission", "status", m}, restReq{method: http.MethodPost, path: "/v1/missions/" + m + "/status", body: `{}`}, "mission.status"},
		{[]string{"plan", "list"}, restReq{method: http.MethodGet, path: "/v1/plans"}, "plan.list"},
		{[]string{"plan", "show", p}, restReq{method: http.MethodGet, path: "/v1/plans/" + p}, "plan.show"},
		{[]string{"workflow", "list"}, restReq{method: http.MethodGet, path: "/v1/workflows"}, "workflow.list"},
		{[]string{"workflow", "show", w}, restReq{method: http.MethodGet, path: "/v1/workflows/" + w}, "workflow.show"},
		{[]string{"reminder", "list"}, restReq{method: http.MethodGet, path: "/v1/reminders"}, "reminder.list"},
		{[]string{"task", "search", "invite"}, restReq{method: http.MethodPost, path: "/v1/tasks/search", body: `{"query":"invite"}`}, "task.search"},
		{[]string{"mission", "search", "invite"}, restReq{method: http.MethodPost, path: "/v1/missions/search", body: `{"query":"invite"}`}, "mission.search"},
		{[]string{"plan", "search", "rollout"}, restReq{method: http.MethodPost, path: "/v1/plans/search", body: `{"query":"rollout"}`}, "plan.search"},
		{[]string{"tasklist", "search", "sprint"}, restReq{method: http.MethodPost, path: "/v1/tasklists/search", body: `{"query":"sprint"}`}, "tasklist.search"},
		{[]string{"workflow", "search", "release"}, restReq{method: http.MethodPost, path: "/v1/workflows/search", body: `{"query":"release"}`}, "workflow.search"},
		{[]string{"task", "board"}, restReq{method: http.MethodPost, path: "/v1/tasks/board", body: `{}`}, "task.board"},
		{[]string{"task", "triage"}, restReq{method: http.MethodPost, path: "/v1/tasks/triage", body: `{}`}, "task.triage"},
		{[]string{"task", "someday"}, restReq{method: http.MethodPost, path: "/v1/tasks/someday", body: `{}`}, "task.someday"},
		{[]string{"task", "deferred"}, restReq{method: http.MethodPost, path: "/v1/tasks/deferred", body: `{}`}, "task.deferred"},
		{[]string{"task", "stale", "--days", "1"}, restReq{method: http.MethodPost, path: "/v1/tasks/stale", body: `{"days":1}`}, "task.stale"},
		{[]string{"task", "history", id}, restReq{method: http.MethodPost, path: "/v1/tasks/" + id + "/history", body: `{}`}, "task.history"},
		{[]string{"task", "tree", "check"}, restReq{method: http.MethodPost, path: "/v1/task/trees/check", body: `{}`}, "task.tree.check"},
		{[]string{"task", "export", "--format", "json"}, restReq{method: http.MethodPost, path: "/v1/tasks/export", body: `{"format":"json"}`}, "task.export"},
		{[]string{"task", "checklist", "list", id}, restReq{method: http.MethodGet, path: "/v1/task/checklists?id=" + id}, "task.checklist.list"},
		{[]string{"task", "time", "show", id}, restReq{method: http.MethodGet, path: "/v1/task/times/" + id}, "task.time.show"},
		{[]string{"task", "recur", "show", rec}, restReq{method: http.MethodGet, path: "/v1/task/recurs/" + rec}, "task.recur.show"},
		{[]string{"task", "template", "list"}, restReq{method: http.MethodGet, path: "/v1/task/templates"}, "task.template.list"},
		{[]string{"task", "template", "show", "ship"}, restReq{method: http.MethodGet, path: "/v1/task/templates/ship"}, "task.template.show"},
		{[]string{"task", "view", "list"}, restReq{method: http.MethodGet, path: "/v1/task/views"}, "task.view.list"},
		{[]string{"task", "view", "show", "urgent"}, restReq{method: http.MethodGet, path: "/v1/task/views/urgent"}, "task.view.show"},
		{[]string{"session", "list"}, restReq{method: http.MethodGet, path: "/v1/sessions"}, "session.list"},
		{[]string{"inbox", "show"}, restReq{method: http.MethodGet, path: "/v1/inboxes/show"}, "inbox.show"},
	}
	for _, r := range reads {
		what := strings.Join(r.cli, " ")
		cli := c.Lore(append(r.cli, "--json")...)
		rep := srv.do(t, r.req)
		if cli.Code != 0 || rep.status != http.StatusOK {
			t.Errorf("%s: CLI exit %d, REST %d: %s / %.300s", what, cli.Code, rep.status, cli.Out(), rep.body)
			continue
		}
		campTasksCheckShape(t, r.opID, rep.body)
		if !restJSONEqual([]byte(cli.Stdout), []byte(rep.body)) {
			t.Errorf("%s: CLI and REST documents differ\nCLI:  %.600s\nREST: %.600s", what, cli.Stdout, rep.body)
		}
	}

	unknown := func(pfx string) string { return pfx + "_" + campTasksUnknownHex }
	refusals := []struct {
		cli  []string
		req  restReq
		code string
	}{
		{[]string{"task", "show", "nope"}, restReq{method: http.MethodGet, path: "/v1/tasks/nope"}, "E_INVALID_ID"},
		{[]string{"task", "show", unknown("tsk")}, restReq{method: http.MethodGet, path: "/v1/tasks/" + unknown("tsk")}, "E_NOT_FOUND"},
		{[]string{"mission", "status", "nope"}, restReq{method: http.MethodPost, path: "/v1/missions/nope/status", body: `{}`}, "E_INVALID_ID"},
		{[]string{"workflow", "delete", "nope"}, restReq{method: http.MethodDelete, path: "/v1/workflows/nope"}, "E_INVALID_ID"},
		{[]string{"reminder", "delete", "nope"}, restReq{method: http.MethodDelete, path: "/v1/reminders/nope"}, "E_INVALID_ID"},
		{[]string{"task", "recur", "stop", "nope"}, restReq{method: http.MethodPost, path: "/v1/task/recurs/nope/stop", body: `{}`}, "E_INVALID_ID"},
		{[]string{"task", "note", "nope", "x"}, restReq{method: http.MethodPost, path: "/v1/tasks/nope/note", body: `{"text":["x"]}`}, "E_INVALID_ID"},
		{[]string{"task", "claim", "nope"}, restReq{method: http.MethodPost, path: "/v1/tasks/nope/claim", body: `{}`}, "E_INVALID_ID"},
		{[]string{"plan", "edit", unknown("pln"), "--title", "x"}, restReq{method: http.MethodPatch, path: "/v1/plans/" + unknown("pln"), body: `{"title":"x"}`}, "E_NOT_FOUND"},
		{[]string{"task", "add", ""}, restReq{method: http.MethodPost, path: "/v1/tasks", body: `{"title":""}`}, "E_INVALID_INPUT"},
		{[]string{"task", "template", "show", "no_such"}, restReq{method: http.MethodGet, path: "/v1/task/templates/no_such"}, "E_NOT_FOUND"},
	}
	for _, r := range refusals {
		what := strings.Join(r.cli, " ")
		cli := c.Lore(append(r.cli, "--json")...)
		ce := restCLIErr(cli)
		rep := srv.do(t, r.req)
		if ce == nil || rep.err == nil {
			t.Errorf("%s: no error document (CLI %s / REST %d %.300s)", what, cli, rep.status, rep.body)
			continue
		}
		if ce.Code != r.code || rep.err.Code != r.code {
			t.Errorf("%s: want %s on both, CLI %s, REST %s (%s)", what, r.code, ce.Code, rep.err.Code, rep.err.Message)
			continue
		}
		if ce.Message != rep.err.Message || ce.Exit != rep.err.Exit || ce.Hint != rep.err.Hint {
			t.Errorf("%s: CLI and REST errors differ\nCLI:  %+v\nREST: %+v", what, *ce, *rep.err)
		}
		def := errs.DefOf(errs.Code(r.code))
		if rep.status != def.HTTP || cli.Code != def.Exit {
			t.Errorf("%s: HTTP %d / exit %d, want %d / %d", what, rep.status, cli.Code, def.HTTP, def.Exit)
		}
	}
}
