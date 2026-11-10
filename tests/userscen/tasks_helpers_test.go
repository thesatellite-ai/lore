//go:build userscen

package userscen

// Shared fixtures of the tasks area (TestTasks*): a person's own repo with
// lore initialised, a two-clone team on one remote, and the --json shapes
// the task, mission, plan, handoff and reminder commands promise. Every
// helper name starts with "tasks" so other areas' files in this package
// never collide with it.

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tasksT0 is the pinned clock every tasks scenario starts at (a Monday).
var tasksT0 = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// tasksSlug is the fake GitHub repo every tasks scenario works in.
const tasksSlug = "github.com/octo-personal/app"

// tasksSolo is one person in their own repo with lore initialised.
type tasksSolo struct {
	w *th.World
	a *th.Actor
	c *th.Clone
}

// newTasksSolo makes alice's world: her own clone of tasksSlug, `lore init`
// done and pushed, the clock pinned at tasksT0.
func newTasksSolo(t *testing.T) *tasksSolo {
	t.Helper()
	scenParallel(t)
	w := newWorld(t)
	w.RequireTestClock()
	a := w.Human("alice", "alice@example.com")
	a.SetNow(tasksT0)
	c := a.Clone(w.Remote(tasksSlug), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	// Commit and push the store the way a person starting a shared repo
	// does; an uncommitted .lore loses its project row once a close lands
	// on main (bugs.md, live/land area).
	c.CommitAll("lore")
	c.AdminPush("origin", "main")
	return &tasksSolo{w: w, a: a, c: c}
}

// tasksTeam is two people with their own clone of one remote, the store
// pushed by alice, live rows fetched fresh on every read.
type tasksTeam struct {
	w        *th.World
	alice    *th.Actor
	bob      *th.Actor
	ca, cb   *th.Clone
	tasklist string
}

// newTasksTeam makes the two-clone world (alice inits and pushes, bob
// clones after).
func newTasksTeam(t *testing.T) *tasksTeam {
	t.Helper()
	scenParallel(t)
	w := newWorld(t)
	w.RequireTestClock()
	tm := &tasksTeam{w: w}
	tm.alice = w.Human("alice", "alice@example.com")
	tm.bob = w.Human("bob", "bob@example.com")
	remote := w.Remote(tasksSlug)
	for _, a := range []*th.Actor{tm.alice, tm.bob} {
		a.WriteConfig(th.Config{Live: th.FreshFetch()})
		a.SetNow(tasksT0)
	}
	tm.ca = tm.alice.Clone(remote, "app")
	tm.ca.MustLore("init", "--non-interactive", "--name=app")
	tm.tasklist = tasksID(t, tm.ca.MustLore("tasklist", "add", "--title", "Inbox", "--body", "team list", "--json"))
	tm.ca.CommitAll("lore")
	tm.ca.AdminPush("origin", "main")
	tm.cb = tm.bob.Clone(remote, "app")
	tm.cb.MustLore("task", "list")
	return tm
}

// tasksEnvelope is the {schema_version, kind, data} envelope of one row.
type tasksEnvelope[T any] struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          T      `json:"data"`
}

// tasksListEnvelope is the list envelope ({…, count, data: [...]}).
type tasksListEnvelope[T any] struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Count         int    `json:"count"`
	Data          []T    `json:"data"`
}

// tasksErrEnvelope is the --json error document.
type tasksErrEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Exit    int    `json:"exit"`
		Class   string `json:"class"`
	} `json:"error"`
}

// tasksTask is the subset of `lore task show --json` the scenarios assert.
type tasksTask struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Body          string   `json:"body"`
	Status        string   `json:"status"`
	Priority      string   `json:"priority"`
	Commitment    string   `json:"commitment"`
	DeferredUntil string   `json:"deferred_until"`
	DueAt         string   `json:"due_at"`
	StartedAt     string   `json:"started_at"`
	CompletedAt   string   `json:"completed_at"`
	MissionID     string   `json:"mission_id"`
	TaskListID    string   `json:"tasklist_id"`
	PlanID        string   `json:"plan_id"`
	ProjectID     string   `json:"project_id"`
	ParentID      string   `json:"parent_id"`
	Rank          string   `json:"rank"`
	CloseReason   string   `json:"close_reason"`
	Assignees     []string `json:"assignees"`
	Watchers      []string `json:"watchers"`
	CreatedAt     string   `json:"created_at"`
}

// tasksIDRe finds a lore id (<prefix>_<32 hex>) in output.
var tasksIDRe = regexp.MustCompile(`\b[a-z]{3}_[0-9a-f]{32}\b`)

// tasksID returns the first lore id a successful command printed.
func tasksID(t *testing.T, r th.Result) string {
	t.Helper()
	r.OK(t)
	id := tasksIDRe.FindString(r.Stdout)
	if id == "" {
		t.Fatalf("no id in output: %s", r)
	}
	return id
}

// tasksAdd adds a task (extra flags appended) and returns its id.
func tasksAdd(t *testing.T, c *th.Clone, title string, extra ...string) string {
	t.Helper()
	r := c.Lore(append([]string{"task", "add", title, "--json"}, extra...)...)
	return th.DecodeJSON[tasksEnvelope[tasksTask]](t, r).Data.ID
}

// tasksShow reads one task through `lore task show --json`.
func tasksShow(t *testing.T, c *th.Clone, id string) tasksTask {
	t.Helper()
	env := th.DecodeJSON[tasksEnvelope[tasksTask]](t, c.Lore("task", "show", id, "--json"))
	if env.Kind != "task.show" || env.SchemaVersion != 1 {
		t.Fatalf("task show --json envelope: kind %q schema %d", env.Kind, env.SchemaVersion)
	}
	return env.Data
}

// tasksList runs `lore task list --json` with extra flags and returns the
// ids in output order.
func tasksList(t *testing.T, c *th.Clone, extra ...string) []string {
	t.Helper()
	env := th.DecodeJSON[tasksListEnvelope[tasksTask]](t, c.Lore(append([]string{"task", "list", "--json"}, extra...)...))
	if env.Count != len(env.Data) {
		t.Fatalf("task list --json count %d but %d rows", env.Count, len(env.Data))
	}
	out := make([]string, 0, len(env.Data))
	for _, d := range env.Data {
		out = append(out, d.ID)
	}
	return out
}

// tasksJSONErr runs a command expected to fail with --json and returns the
// decoded error after checking the exit code matches the document.
func tasksJSONErr(t *testing.T, c *th.Clone, args ...string) tasksErrEnvelope {
	t.Helper()
	r := c.Lore(append(args, "--json")...)
	if r.Code == 0 {
		t.Fatalf("expected a refusal: %s", r)
	}
	var e tasksErrEnvelope
	if err := json.Unmarshal([]byte(r.Stdout), &e); err != nil || e.Error.Code == "" {
		t.Fatalf("--json error document on stdout (%v): %s", err, r)
	}
	if e.Error.Exit != r.Code {
		t.Fatalf("error document says exit %d, process exited %d: %s", e.Error.Exit, r.Code, r)
	}
	return e
}

// tasksHas reports whether id is in ids.
func tasksHas(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
