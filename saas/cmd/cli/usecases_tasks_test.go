package main

// Use-case tests for task / mission / archive / decision / hotfix / reminder
// commands and their helpers. Command-level cases run the real binary through
// the e2e helpers (sync_e2e_test.go); pure helpers are unit-tested directly.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbent"
	"dbent/gen/ent"
	entReminder "dbent/gen/ent/reminder"
	entTask "dbent/gen/ent/task"
)

// ucProject is a fresh lore project in a clone of an isolated world.
func ucProject(t *testing.T) *clone {
	t.Helper()
	w := newWorld(t)
	c := w.newClone("dev")
	c.lore("init", "--non-interactive", "--name=uc")
	return c
}

// ucJSON decodes a command's JSON envelope into v (the "data" payload).
func ucJSON(t *testing.T, r result, v any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
		t.Fatalf("not a JSON envelope: %v\n%s", err, r.stdout)
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		t.Fatalf("data: %v\n%s", err, env.Data)
	}
}

// ucID runs a create command and returns the id it printed.
func ucID(t *testing.T, c *clone, args ...string) string {
	t.Helper()
	id := idPattern.FindString(c.lore(args...).stdout)
	if id == "" {
		t.Fatalf("lore %v printed no id", args)
	}
	return id
}

func ucTasklist(t *testing.T, c *clone) string {
	t.Helper()
	var tl struct {
		ID string `json:"id"`
	}
	ucJSON(t, c.lore("tasklist", "add", "--title=Sprint", "--body=backlog", "--json"), &tl)
	return tl.ID
}

// ucScalar reads one value from the project's DB.
func ucScalar(t *testing.T, c *clone, query string, args ...any) string {
	t.Helper()
	db := dbent.InitDB(filepath.Join(c.dir, ".lore", "lore.db"))
	defer func() { _ = db.Close() }() // read-only probe
	var v *string
	if err := db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if v == nil {
		return ""
	}
	return *v
}

type ucBrief struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Commitment    string `json:"commitment"`
	DeferredUntil string `json:"deferred_until"`
	DueAt         string `json:"due_at"`
	MissionID     string `json:"mission_id"`
}

func ucListTitles(t *testing.T, c *clone, args ...string) []string {
	t.Helper()
	var rows []ucBrief
	ucJSON(t, c.lore(append([]string{"task", "list", "--json"}, args...)...), &rows)
	titles := make([]string, 0, len(rows))
	for _, r := range rows {
		titles = append(titles, r.Title)
	}
	return titles
}

// Every filter of `task list` and the triage / someday / deferred views
// (runTaskList, activeTaskFilter semantics, printTaskRows, runTaskView,
// briefFromTask via --json).
func TestUC_Tasks_ListFiltersAndViews(t *testing.T) {
	c := ucProject(t)
	tl := ucTasklist(t, c)
	m := ucID(t, c, "mission", "add", "Ship")
	c.lore("task", "add", "active one", "--tasklist="+tl, "--mission="+m, "--due=2026-05-01", "--commitment=accepted")
	c.lore("task", "add", "proposed one", "--tasklist="+tl, "--commitment=proposed")
	c.lore("task", "add", "someday one", "--tasklist="+tl, "--commitment=someday")
	c.lore("task", "add", "deferred one", "--tasklist="+tl, "--defer-until=2099-01-01")
	c.lore("task", "add", "woke up", "--tasklist="+tl, "--defer-until=2020-01-01")
	doneID := ucID(t, c, "task", "add", "finished", "--tasklist="+tl)
	c.lore("task", "done", doneID)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "active one,woke up"},
		{[]string{"--include-proposed"}, "active one,proposed one,woke up"},
		{[]string{"--include-someday"}, "active one,someday one,woke up"},
		{[]string{"--include-deferred"}, "active one,deferred one,woke up"},
		{[]string{"--all"}, "active one,deferred one,finished,proposed one,someday one,woke up"},
		{[]string{"--status=done"}, "finished"},
		{[]string{"--all", "--status=todo"}, "active one,deferred one,proposed one,someday one,woke up"},
		{[]string{"--commitment=proposed"}, "proposed one"},
		{[]string{"--mission=" + m}, "active one"},
	} {
		got := ucListTitles(t, c, tc.args...)
		sortStrings(got)
		if strings.Join(got, ",") != tc.want {
			t.Fatalf("task list %v = %v, want %s", tc.args, got, tc.want)
		}
	}
	if r := c.loreAny("task", "list", "--commitment=maybe"); r.code == 0 || !strings.Contains(r.stderr, "E_INVALID_INPUT") {
		t.Fatalf("a bad --commitment must be a hard error: %d %s", r.code, r.stderr)
	}

	var rows []ucBrief
	ucJSON(t, c.lore("task", "list", "--json", "--include-deferred"), &rows)
	for _, r := range rows {
		switch r.Title {
		case "active one":
			if r.DueAt != "2026-05-01" || r.MissionID != m || r.Commitment != "accepted" || r.Status != "todo" {
				t.Fatalf("brief of active task: %+v", r)
			}
		case "deferred one":
			if r.DeferredUntil != "2099-01-01" {
				t.Fatalf("brief lost deferred_until: %+v", r)
			}
		}
	}

	human := c.lore("task", "list", "--all").stdout
	for _, want := range []string{"active one [medium] due:2026-05-01", "(proposed)", "(someday)", "⏾2099-01-01"} {
		if !strings.Contains(human, want) {
			t.Fatalf("human list misses %q:\n%s", want, human)
		}
	}
	if out := c.lore("task", "list", "--mission=msn_00000000000070008000000000000000").stdout; !strings.Contains(out, "(no tasks)") {
		t.Fatalf("empty list must say so: %q", out)
	}
	for view, want := range map[string]string{"triage": "proposed one", "someday": "someday one", "deferred": "deferred one"} {
		out := c.lore("task", view).stdout
		if !strings.Contains(out, want) || strings.Count(out, "\n") != 1 {
			t.Fatalf("task %s = %q, want only %q", view, out, want)
		}
	}
}

// start / done / cancel (updateTaskStatus) with their side effects, and show
// in both forms (lookupTask, fullFromTask).
func TestUC_Tasks_StatusTransitionsAndShow(t *testing.T) {
	c := ucProject(t)
	tl := ucTasklist(t, c)
	m := ucID(t, c, "mission", "add", "Ship")
	id := ucID(t, c, "task", "add", "snoozed idea", "--tasklist="+tl, "--mission="+m, "--commitment=proposed",
		"--defer-until=2099-01-01", "--due=2026-06-01", "--body=details here")

	if out := c.lore("task", "start", id).stdout; !strings.Contains(out, id+" started") {
		t.Fatalf("start: %q", out)
	}
	var full struct {
		Status, Commitment, StartedAt, CompletedAt, DeferredUntil string
		Body, DueAt, MissionID, TasklistID, ProjectID, CreatedAt  string
	}
	show := func() {
		t.Helper()
		var raw map[string]string
		ucJSON(t, c.lore("task", "show", id, "--json"), &raw)
		full.Status, full.Commitment = raw["status"], raw["commitment"]
		full.StartedAt, full.CompletedAt, full.DeferredUntil = raw["started_at"], raw["completed_at"], raw["deferred_until"]
		full.Body, full.DueAt, full.MissionID = raw["body"], raw["due_at"], raw["mission_id"]
		full.TasklistID, full.ProjectID, full.CreatedAt = raw["tasklist_id"], raw["project_id"], raw["created_at"]
	}
	show()
	if full.Status != "in_progress" || full.Commitment != "accepted" || full.DeferredUntil != "" || full.StartedAt == "" {
		t.Fatalf("start must promote and un-defer: %+v", full)
	}
	if full.Body != "details here" || full.DueAt != "2026-06-01" || full.MissionID != m || full.TasklistID != tl || full.ProjectID == "" || full.CreatedAt == "" {
		t.Fatalf("show --json lost fields: %+v", full)
	}
	c.lore("task", "done", id)
	show()
	if full.Status != "done" || full.CompletedAt == "" {
		t.Fatalf("done: %+v", full)
	}
	c.lore("task", "cancel", id)
	show()
	if full.Status != "cancelled" {
		t.Fatalf("cancel: %+v", full)
	}

	human := c.lore("task", "show", id).stdout
	for _, want := range []string{"title:    snoozed idea", "status:   cancelled", "commit:   accepted", "due:      2026-06-01", "mission:  " + m, "details here"} {
		if !strings.Contains(human, want) {
			t.Fatalf("show misses %q:\n%s", want, human)
		}
	}
	deferred := ucID(t, c, "task", "add", "later", "--tasklist="+tl, "--defer-until=2099-02-02")
	if out := c.lore("task", "show", deferred).stdout; !strings.Contains(out, "deferred: 2099-02-02") {
		t.Fatalf("show must print the deferral: %q", out)
	}
	for _, verb := range []string{"show", "start", "done", "cancel"} {
		r := c.loreAny("task", verb, "tsk_00000000000070008000000000000000")
		if r.code == 0 || !strings.Contains(r.stderr, "E_NOT_FOUND") {
			t.Fatalf("task %s on an unknown id: %d %s", verb, r.code, r.stderr)
		}
	}
}

// edit / add validation of --commitment, --defer-until, --status
// (parseCommitment, parseDeferUntil) and un-deferring.
func TestUC_Tasks_EditValidationAndDeferral(t *testing.T) {
	c := ucProject(t)
	tl := ucTasklist(t, c)
	id := ucID(t, c, "task", "add", "edit me", "--tasklist="+tl)
	for _, args := range [][]string{
		{"task", "edit", id, "--commitment=maybe"},
		{"task", "edit", id, "--defer-until=next week"},
		{"task", "edit", id, "--status=paused"},
		{"task", "add", "x", "--tasklist=" + tl, "--commitment=maybe"},
		{"task", "add", "x", "--tasklist=" + tl, "--defer-until=31/12/2099"},
	} {
		if r := c.loreAny(args...); r.code == 0 || !strings.Contains(r.stderr, "E_INVALID_INPUT") {
			t.Fatalf("%v must be refused: %d %s", args, r.code, r.stderr)
		}
	}
	c.lore("task", "edit", id, "--defer-until=2099-03-03", "--priority=high", "--commitment=someday")
	if got := ucListTitles(t, c, "--include-someday"); len(got) != 0 {
		t.Fatalf("a future-deferred task must be hidden: %v", got)
	}
	c.lore("task", "edit", id, "--clear-defer", "--commitment=accepted")
	if got := ucListTitles(t, c); strings.Join(got, ",") != "edit me" {
		t.Fatalf("--clear-defer must bring it back: %v", got)
	}
	if r := c.loreAny("task", "edit", "tsk_00000000000070008000000000000000", "--priority=low"); r.code == 0 {
		t.Fatal("edit of an unknown task must fail")
	}
}

// task search: relations (briefIfPresent*) and the active-only default
// (activeTaskFilter) with --all to widen.
func TestUC_Tasks_SearchRelationsAndActiveFilter(t *testing.T) {
	c := ucProject(t)
	tl := ucTasklist(t, c)
	m := ucID(t, c, "mission", "add", "Ship")
	p := ucID(t, c, "plan", "add", "--title=Roadmap", "--body=q3")
	c.lore("task", "add", "zebra wiring", "--tasklist="+tl, "--mission="+m, "--plan="+p, "--body=stripes")
	c.lore("task", "add", "zebra idea", "--tasklist="+tl, "--commitment=proposed")
	done := ucID(t, c, "task", "add", "zebra finished", "--tasklist="+tl)
	c.lore("task", "done", done)

	type hit struct {
		Row       map[string]any            `json:"row"`
		Relations map[string]map[string]any `json:"relations"`
	}
	var hits []hit
	ucJSON(t, c.lore("task", "search", "zebra", "--json"), &hits)
	if len(hits) != 1 || hits[0].Row["title"] != "zebra wiring" {
		t.Fatalf("default search must return active tasks only: %+v", hits)
	}
	rel := hits[0].Relations
	if rel["tasklist"]["id"] != tl || rel["tasklist"]["title"] != "Sprint" ||
		rel["mission"]["id"] != m || rel["mission"]["status"] != "active" ||
		rel["plan"]["id"] != p || rel["plan"]["title"] != "Roadmap" || hits[0].Row["body"] != "stripes" {
		t.Fatalf("relations: %+v row %+v", rel, hits[0].Row)
	}
	ucJSON(t, c.lore("task", "search", "zebra", "--json", "--all"), &hits)
	if len(hits) != 3 {
		t.Fatalf("--all must include proposed and done hits: %d", len(hits))
	}
	for _, h := range hits {
		if h.Row["title"] == "zebra idea" && (h.Relations["mission"] != nil || h.Relations["plan"] != nil || h.Relations["tasklist"] == nil) {
			t.Fatalf("absent relations must be null: %+v", h.Relations)
		}
	}
}

// mission pause / resume / done (setMissionStatus and the done command),
// mission show --json tasks (briefFromTask).
func TestUC_Tasks_MissionLifecycle(t *testing.T) {
	c := ucProject(t)
	tl := ucTasklist(t, c)
	m := ucID(t, c, "mission", "add", "Ship", "--target=2026-09-01")
	c.lore("task", "add", "piece", "--tasklist="+tl, "--mission="+m, "--due=2026-08-01", "--defer-until=2099-01-01")
	// mission list --json embeds tasks in the brief shape (briefFromTask).
	status := func() string {
		var ms []struct {
			ID     string    `json:"id"`
			Status string    `json:"status"`
			Tasks  []ucBrief `json:"tasks"`
		}
		ucJSON(t, c.lore("mission", "list", "--all", "--json"), &ms)
		if len(ms) != 1 || ms[0].ID != m {
			t.Fatalf("mission list: %+v", ms)
		}
		tk := ms[0].Tasks
		if len(tk) != 1 || tk[0].DueAt != "2026-08-01" || tk[0].DeferredUntil != "2099-01-01" || tk[0].MissionID != m {
			t.Fatalf("mission tasks: %+v", tk)
		}
		return ms[0].Status
	}
	for _, step := range []struct{ verb, label, want string }{
		{"pause", "paused", "paused"},
		{"resume", "resumed", "active"},
		{"done", "", "done"},
	} {
		out := c.lore("mission", step.verb, m).stdout
		if step.label != "" && !strings.Contains(out, m+" "+step.label) {
			t.Fatalf("mission %s: %q", step.verb, out)
		}
		if got := status(); got != step.want {
			t.Fatalf("after %s status = %s", step.verb, got)
		}
	}
	if r := c.loreAny("mission", "pause", "msn_00000000000070008000000000000000"); r.code == 0 || !strings.Contains(r.stderr, "E_NOT_FOUND") {
		t.Fatalf("unknown mission: %d %s", r.code, r.stderr)
	}
	if r := c.loreAny("mission", "resume", m, "--read-only"); r.code == 0 || !strings.Contains(r.stderr, "E_READ_ONLY") {
		t.Fatalf("read-only must refuse: %d %s", r.code, r.stderr)
	}
}

// archive / unarchive across kinds (runArchive, runUnarchive), including the
// flags read back by extractCommon (--db, --read-only).
func TestUC_Tasks_ArchiveUnarchive(t *testing.T) {
	c := ucProject(t)
	ids := map[string]string{
		"memories":  ucID(t, c, "memory", "add", "--body", "keep me"),
		"rules":     ucID(t, c, "rule", "add", "--severity=must", "--body", "always x"),
		"decisions": ucID(t, c, "decision", "add", "--title=pick", "--body", "chose y"),
		"hotfixes":  ucID(t, c, "hotfix", "add", "--title=careful", "--body", "beware z"),
		"patterns":  ucID(t, c, "pattern", "add", "--title=opt", "--body", "options"),
	}
	kinds := map[string]string{"memories": "memory", "rules": "rule", "decisions": "decision", "hotfixes": "hotfix", "patterns": "pattern"}
	for table, id := range ids {
		kind := kinds[table]
		if out := c.lore(kind, "archive", id).stdout; !strings.Contains(out, id+" archived") {
			t.Fatalf("%s archive: %q", kind, out)
		}
		if ucScalar(t, c, "SELECT CAST(archived_at AS TEXT) FROM "+table+" WHERE id = ?", id) == "" {
			t.Fatalf("%s archive did not set archived_at", kind)
		}
		if out := c.lore(kind, "unarchive", id).stdout; !strings.Contains(out, id+" unarchived") {
			t.Fatalf("%s unarchive: %q", kind, out)
		}
		if ucScalar(t, c, "SELECT CAST(archived_at AS TEXT) FROM "+table+" WHERE id = ?", id) != "" {
			t.Fatalf("%s unarchive left archived_at", kind)
		}
	}
	mem := ids["memories"]
	c.lore("memory", "archive", mem)
	var list []struct {
		ID string `json:"id"`
	}
	ucJSON(t, c.lore("memory", "list", "--json"), &list)
	if len(list) != 0 {
		t.Fatalf("archived memory listed: %+v", list)
	}
	ucJSON(t, c.lore("memory", "list", "--json", "--archived"), &list)
	if len(list) != 1 || list[0].ID != mem {
		t.Fatalf("--archived must show it: %+v", list)
	}
	// --db is honoured from anywhere (extractCommon); --read-only refuses.
	elsewhere := t.TempDir()
	db := filepath.Join(c.dir, ".lore", "lore.db")
	if r := c.w.run(elsewhere, nil, e2eBin, "memory", "unarchive", mem, "--db", db); r.code != 0 {
		t.Fatalf("--db from another directory: %s", r.stderr)
	}
	if r := c.loreAny("memory", "archive", mem, "--read-only"); r.code == 0 || !strings.Contains(r.stderr, "E_READ_ONLY") {
		t.Fatalf("archive --read-only: %d %s", r.code, r.stderr)
	}
	if r := c.loreAny("memory", "unarchive", mem, "--read-only"); r.code == 0 || !strings.Contains(r.stderr, "E_READ_ONLY") {
		t.Fatalf("unarchive --read-only: %d %s", r.code, r.stderr)
	}
	for _, bad := range []string{"mem_00000000000070008000000000000000", "not-an-id"} {
		for _, verb := range []string{"archive", "unarchive"} {
			if r := c.loreAny("memory", verb, bad); r.code == 0 || !strings.Contains(r.stderr, "E_NOT_FOUND") {
				t.Fatalf("memory %s %s: %d %s", verb, bad, r.code, r.stderr)
			}
		}
	}
}

// decision add / hotfix add (runDecisionAdd, decisionStatus, runHotfixAdd,
// hotfixSeverity) and created-by resolution on the generated add commands
// (resolveCreatedBy).
func TestUC_Tasks_DecisionHotfixAndCreatedBy(t *testing.T) {
	c := ucProject(t)
	for status, want := range map[string]string{"proposed": "proposed", "deprecated": "deprecated", "not-a-status": "accepted"} {
		id := ucID(t, c, "decision", "add", "--title=t "+status, "--status="+status, "--source-ref=pr#1", "--body", "because")
		if got := ucScalar(t, c, "SELECT status FROM decisions WHERE id = ?", id); got != want {
			t.Fatalf("decision --status=%s stored %q, want %q", status, got, want)
		}
		if ucScalar(t, c, "SELECT source_ref FROM decisions WHERE id = ?", id) != "pr#1" {
			t.Fatal("decision --source-ref lost")
		}
	}
	for sev, want := range map[string]string{"low": "low", "critical": "critical", "loud": "high"} {
		r := c.lore("hotfix", "add", "--title=h "+sev, "--severity="+sev, "--body", "watch out")
		id := idPattern.FindString(r.stdout)
		if got := ucScalar(t, c, "SELECT severity FROM hotfixes WHERE id = ?", id); got != want {
			t.Fatalf("hotfix --severity=%s stored %q, want %q", sev, got, want)
		}
	}
	for _, kind := range []string{"decision", "hotfix"} {
		if r := c.loreAny(kind, "add", "--title=t", "--body", "   "); r.code == 0 || !strings.Contains(r.stderr, "E_EMPTY_BODY") {
			t.Fatalf("%s add with a blank body: %d %s", kind, r.code, r.stderr)
		}
		if r := c.loreAny(kind, "add", "--title=t", "--body", "b", "--read-only"); r.code == 0 || !strings.Contains(r.stderr, "E_READ_ONLY") {
			t.Fatalf("%s add --read-only: %d %s", kind, r.code, r.stderr)
		}
		if r := c.loreAny(kind, "add", "--title=t", "--body", "b", "--created-by=act_00000000000070008000000000000000"); r.code == 0 || !strings.Contains(r.stderr, "E_NOT_FOUND") {
			t.Fatalf("%s add with an unknown --created-by: %d %s", kind, r.code, r.stderr)
		}
	}
	// Generated add commands: default actor, explicit actor, unknown actor.
	p := ucID(t, c, "pattern", "add", "--title=p1", "--body", "x")
	actor := ucScalar(t, c, "SELECT created_by_actor_id FROM patterns WHERE id = ?", p)
	if !strings.HasPrefix(actor, "act_") {
		t.Fatalf("default created_by must be the current actor: %q", actor)
	}
	// A second actor (another identity), then attribute a row to it while
	// running as the first.
	c.env = []string{"LORE_ACTOR=reviewer-bot"}
	other := ucScalar(t, c, "SELECT created_by_actor_id FROM patterns WHERE id = ?", ucID(t, c, "pattern", "add", "--title=by other", "--body", "o"))
	c.env = nil
	if other == actor || !strings.HasPrefix(other, "act_") {
		t.Fatalf("LORE_ACTOR must yield a distinct actor: %q vs %q", other, actor)
	}
	p2 := ucID(t, c, "pattern", "add", "--title=p2", "--body", "y", "--created-by="+other)
	if got := ucScalar(t, c, "SELECT created_by_actor_id FROM patterns WHERE id = ?", p2); got != other {
		t.Fatalf("explicit --created-by stored %q, want %q", got, other)
	}
	if r := c.loreAny("pattern", "add", "--title=p3", "--body", "z", "--created-by=act_00000000000070008000000000000000"); r.code == 0 || !strings.Contains(r.stderr, "E_NOT_FOUND") {
		t.Fatalf("unknown --created-by on a generated add: %d %s", r.code, r.stderr)
	}
}

// reminder done: recurring reminders reschedule (bumpRecurrence), one-off
// ones complete.
func TestUC_Tasks_ReminderRecurrence(t *testing.T) {
	c := ucProject(t)
	rec := ucID(t, c, "reminder", "add", "rotate keys", "--due=2026-03-10", "--recurrence=1m")
	if out := c.lore("reminder", "done", rec).stdout; !strings.Contains(out, "rescheduled to 2026-04-10") {
		t.Fatalf("recurring done: %q", out)
	}
	once := ucID(t, c, "reminder", "add", "renew domain", "--due=2026-03-11")
	if out := c.lore("reminder", "done", once).stdout; !strings.Contains(out, "completed") {
		t.Fatalf("one-off done: %q", out)
	}
	var pending, done []struct {
		ID    string `json:"id"`
		DueAt string `json:"due_at"`
	}
	ucJSON(t, c.lore("reminder", "list", "--json"), &pending)
	ucJSON(t, c.lore("reminder", "list", "--done", "--json"), &done)
	if len(pending) != 1 || pending[0].ID != rec || !strings.HasPrefix(pending[0].DueAt, "2026-04-10") {
		t.Fatalf("pending: %+v", pending)
	}
	if len(done) != 1 || done[0].ID != once {
		t.Fatalf("done: %+v", done)
	}
	if r := c.loreAny("reminder", "add", "x", "--due=2026-03-10", "--recurrence=fortnightly"); r.code == 0 || !strings.Contains(r.stderr, "E_INVALID_INPUT") {
		t.Fatalf("bad --recurrence: %d %s", r.code, r.stderr)
	}
}

func TestUC_Tasks_BumpRecurrence(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	want := map[entReminder.Recurrence]string{
		entReminder.Recurrence7d:  "2026-03-17",
		entReminder.Recurrence30d: "2026-04-09",
		entReminder.Recurrence1m:  "2026-04-10",
		entReminder.Recurrence3m:  "2026-06-10",
		entReminder.Recurrence6m:  "2026-09-10",
		entReminder.Recurrence1y:  "2027-03-10",
	}
	if len(want) != len(allRecurrenceValues()) {
		t.Fatalf("test covers %d patterns, enum has %d", len(want), len(allRecurrenceValues()))
	}
	for p, w := range want {
		if got := bumpRecurrence(from, p).Format(time.DateOnly); got != w {
			t.Fatalf("%s: %s, want %s", p, got, w)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an unknown pattern must panic (invariant)")
		}
	}()
	bumpRecurrence(from, entReminder.Recurrence("2w"))
}

func TestUC_Tasks_PureHelpers(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"accepted", "proposed", "someday"} {
		if c, err := parseCommitment(s); err != nil || string(c) != s {
			t.Fatalf("parseCommitment(%s) = %q %v", s, c, err)
		}
	}
	if _, err := parseCommitment("maybe"); err == nil {
		t.Fatal("parseCommitment must reject unknown values")
	}
	if d, err := parseDeferUntil("2099-01-02"); err != nil || d.Format(time.DateOnly) != "2099-01-02" {
		t.Fatalf("parseDeferUntil: %v %v", d, err)
	}
	if _, err := parseDeferUntil("tomorrow"); err == nil {
		t.Fatal("parseDeferUntil must reject non-dates")
	}
	if taskStatus("in_progress") != entTask.StatusInProgress || taskStatus("bogus") != entTask.StatusTodo {
		t.Fatal("taskStatus parse/fallback")
	}
	seen := map[string]bool{}
	for _, s := range []string{"done", "in_progress", "cancelled", "blocked", "todo"} {
		g := taskStatusStyle(s)
		if g == "" || seen[g] && s != "todo" {
			t.Fatalf("status %s has no distinct glyph: %q", s, g)
		}
		seen[g] = true
	}
	if !strings.Contains(taskStatusStyle("done"), "✓") || !strings.Contains(taskStatusStyle("blocked"), "⛔") || !strings.Contains(taskStatusStyle("weird"), "○") {
		t.Fatal("taskStatusStyle glyphs")
	}
	if derefStr(nil) != "" || derefTime(nil) != "" {
		t.Fatal("nil derefs must be empty")
	}
	s, when := "x", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if derefStr(&s) != "x" || derefTime(&when) != "2026-01-02T03:04:05Z" {
		t.Fatalf("derefs: %q %q", derefStr(&s), derefTime(&when))
	}
	if briefIfPresent(nil) != nil || briefIfPresentMission(nil) != nil || briefIfPresentPlan(nil) != nil {
		t.Fatal("absent edges must be nil")
	}
	tlb := briefIfPresent(&ent.TaskList{ID: "tlt_1", Title: "L"}).(map[string]any)
	mb := briefIfPresentMission(&ent.Mission{ID: "msn_1", Title: "M", Status: "active"}).(map[string]any)
	pb := briefIfPresentPlan(&ent.Plan{ID: "pln_1", Title: "P"}).(map[string]any)
	if tlb["id"] != "tlt_1" || tlb["title"] != "L" || mb["status"] != "active" || mb["title"] != "M" || pb["id"] != "pln_1" || pb["title"] != "P" {
		t.Fatalf("briefs: %v %v %v", tlb, mb, pb)
	}
	due, plan, repo, mission, tl, body := when, "pln_1", "rep_1", "msn_1", "tlt_1", "b"
	full := fullFromTask(&ent.Task{ID: "tsk_1", Title: "T", Status: entTask.StatusDone, Priority: entTask.PriorityHigh,
		Commitment: entTask.CommitmentAccepted, ProjectID: "prj_1", CreatedAt: when, DueAt: &due, StartedAt: &due,
		CompletedAt: &due, DeferredUntil: &due, PlanID: &plan, RepoID: &repo, MissionID: &mission, TasklistID: &tl, Body: &body})
	if full.PlanID != plan || full.RepoID != repo || full.MissionID != mission || full.TaskListID != tl || full.Body != body ||
		full.DueAt != "2026-01-02" || full.DeferredUntil != "2026-01-02" || full.StartedAt == "" || full.CompletedAt == "" || full.Priority != "high" {
		t.Fatalf("fullFromTask: %+v", full)
	}
	brief := briefFromTask(&ent.Task{ID: "tsk_2", Title: "B", Status: entTask.StatusTodo, Commitment: entTask.CommitmentSomeday,
		DueAt: &due, DeferredUntil: &due, MissionID: &mission})
	if brief.DueAt != "2026-01-02" || brief.DeferredUntil != "2026-01-02" || brief.MissionID != mission || brief.Commitment != "someday" {
		t.Fatalf("briefFromTask: %+v", brief)
	}
}
