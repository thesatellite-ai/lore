package mission_test

import (
	"encoding/json"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// addMission adds a mission and returns its id.
func addMission(t *testing.T, h *opstest.H, in string) string {
	t.Helper()
	var w struct{ Data struct{ ID string } }
	if err := json.Unmarshal([]byte(h.JSON(h.Must("mission.add", in))), &w); err != nil {
		t.Fatal(err)
	}
	return w.Data.ID
}

// TestMissionLifecycle compares with lore 0.1.17's output for the same
// commands: "✓ <id> — <title>", the --key re-run line, list "<id>
// [<status>] <title>   (<n> tasks)" over the open set (--all adds done),
// show's block with Tasks:, pause/resume/done lines.
func TestMissionLifecycle(t *testing.T) {
	h := opstest.New(t)
	add := h.Must("mission.add", `{"title":"Ship v2","body":"the big one","target":"2099-01-02","key":"ship-v2"}`)
	id := addMission(t, h, `{"title":"Ship v2","key":"ship-v2"}`) // the --key re-run
	if got := opstest.Text(add); got != "✓ "+id+" — Ship v2\n" {
		t.Fatalf("add %q", got)
	}
	again := h.Must("mission.add", `{"title":"Ship v2","key":"ship-v2"}`)
	if got := opstest.Text(again); got != "✓ "+id+" — Ship v2 (already added with --key ship-v2)\n" {
		t.Fatalf("re-add %q", got)
	}
	other := addMission(t, h, `{"title":"Other"}`)
	if other == id {
		t.Fatal("a mission without --key reused the keyed one")
	}
	child := h.Seed(schema.TableTasks, map[string]string{"title": `"child"`, "mission_id": opstest.Quote(id)})

	want := id + " [active] Ship v2   (1 tasks)\n" + other + " [active] Other   (0 tasks)\n"
	if got := opstest.Text(h.Must("mission.list", ``)); !sameLines(got, want) {
		t.Fatalf("list\n got %q\nwant %q", got, want)
	}
	show := h.Must("mission.show", `{"id":"`+id+`"}`)
	wantShow := id + "\n  title:  Ship v2\n  status: active\n  target: 2099-01-02\n\nthe big one\n\nTasks:\n  " + child + " [todo] child\n" +
		contenttest.WriterLine(t, h.JSON(show), "data") // INTEG-AUDIT-2: ends with the writers line
	if got := opstest.Text(show); got != wantShow {
		t.Fatalf("show\n got %q\nwant %q", got, wantShow)
	}
	var sj struct {
		Kind  string `json:"kind"`
		Count int    `json:"count"`
		Data  struct {
			IdempotencyKey string            `json:"idempotency_key"`
			Tasks          []json.RawMessage `json:"tasks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(show)), &sj); err != nil {
		t.Fatal(err)
	}
	if sj.Kind != "mission.show" || sj.Count != 1 || len(sj.Data.Tasks) != 1 || sj.Data.IdempotencyKey != "ship-v2" {
		t.Fatalf("show json: count must be len(tasks) %s", h.JSON(show))
	}

	for _, step := range []struct{ op, line string }{{"mission.pause", "paused"}, {"mission.resume", "resumed"}, {"mission.done", "completed"}} {
		// done names the open child (A-17, done_open_test.go); the first line is the same
		if got := opstest.Text(h.Must(step.op, `{"id":"`+id+`"}`)); !strings.HasPrefix(got, "✓ "+id+" "+step.line+"\n") {
			t.Fatalf("%s: %q", step.op, got)
		}
	}
	if got := opstest.Text(h.Must("mission.list", ``)); got != other+" [active] Other   (0 tasks)\n" {
		t.Fatalf("open set after done: %q", got)
	}
	if got := opstest.Text(h.Must("mission.list", `{"all":true}`)); !strings.Contains(got, id+" [done] Ship v2   (1 tasks)\n") {
		t.Fatalf("--all: %q", got)
	}
	if got := opstest.Text(h.Must("mission.list", `{"status":"done"}`)); got != id+" [done] Ship v2   (1 tasks)\n" {
		t.Fatalf("--status done: %q", got)
	}
	if !strings.Contains(h.JSON(h.Must("mission.show", `{"id":"`+id+`"}`)), `"completed_at":"`) {
		t.Fatal("done did not stamp completed_at")
	}
	if got := opstest.Text(h.Must("mission.search", `{"query":"ship"}`)); !strings.Contains(got, id) {
		t.Fatalf("search %q", got)
	}
}

// TestMissionDeleteClearsTasks: deleting a mission takes its tasks into
// the trash in the same batch (DELETION 3.4: tasks are a mission's
// dependents; this used to pin the old SetNull behaviour).
func TestMissionDeleteClearsTasks(t *testing.T) {
	h := opstest.New(t)
	id := addMission(t, h, `{"title":"Doomed"}`)
	child := h.Seed(schema.TableTasks, map[string]string{"mission_id": opstest.Quote(id)})
	h.Fails("mission.delete", `{"id":"`+id+`"}`, errs.NeedsConfirm)
	h.MustYes("mission.delete", `{"id":"`+id+`"}`)
	h.Fails("mission.show", `{"id":"`+id+`"}`, errs.InTrash)
	h.Fails("task.show", `{"id":"`+child+`"}`, errs.InTrash) // DELETION 3.4: its tasks went to the trash with it
}

// TestMissionRefusals: empty title, bad target date, the status closed
// set on edit, approvers act_ only without a host roster, --plan must be a
// pln_ id, nothing to change.
func TestMissionRefusals(t *testing.T) {
	h := opstest.New(t)
	h.Fails("mission.add", `{"title":"   "}`, errs.InvalidInput)
	h.Fails("mission.add", `{"title":"x","target":"next week"}`, errs.Date) // TASK 12: phrase table, E_DATE
	h.Fails("mission.add", `{"title":"x","plan":"tsk_01a1a68b3b5f788ea696d4323f621ff6"}`, errs.InvalidID)
	if _, err := h.Run("mission.add", `{"title":"x","approvers":["@alice"]}`); err == nil {
		t.Fatal("@handle approver accepted without a roster")
	}
	approver := opstest.Actor
	id := addMission(t, h, `{"title":"x","approvers":["`+approver+`"]}`)
	if !strings.Contains(h.JSON(h.Must("mission.show", `{"id":"`+id+`"}`)), `"approvers":["`+approver+`"]`) {
		t.Fatal("act_ approver not stored")
	}
	for _, bad := range []string{"open", "Active", "finished"} {
		h.Fails("mission.edit", `{"id":"`+id+`","status":"`+bad+`"}`, errs.InvalidInput)
	}
	for _, ok := range schema.MissionStatuses() {
		h.Must("mission.edit", `{"id":"`+id+`","status":"`+ok+`"}`)
	}
	h.Fails("mission.edit", `{"id":"`+id+`"}`, errs.InvalidInput)
	h.Fails("mission.show", `{"id":"msn_bad"}`, errs.InvalidID)
	pln := h.Seed(schema.TablePlans, nil)
	withPlan := addMission(t, h, `{"title":"carries a plan","plan":"`+pln+`"}`)
	if !strings.Contains(h.JSON(h.Must("mission.show", `{"id":"`+withPlan+`"}`)), `"plan_id":"`+pln+`"`) {
		t.Fatal("--plan not stored")
	}
}

// planFile is the v0.1.17 plan file the apply test runs (the same file
// produced the lines checked below with `lore plan apply` 0.1.17).
const planFile = `mission:
  key: invite
  title: Invite flow
tasks:
  - key: backend
    title: Backend API
    timebox: 2h
  - key: ui
    title: Invite screen
    after: [backend:merged]
    gate: required
  - key: docs
    title: Docs
    after: [backend, ui]
    timebox: 90m
`

// TestMissionApply: the plan file creates the mission and its tasks with
// v0.1.17's lines (title padded to 30, timebox, after, gate); a re-run
// says "found" and adds nothing; after: becomes blocked_by edges.
func TestMissionApply(t *testing.T) {
	h := opstest.New(t)
	path := filepath.Join(t.TempDir(), "plan.yaml")
	if err := os.WriteFile(path, []byte(planFile), 0o600); err != nil {
		t.Fatal(err)
	}
	first := h.Must("mission.apply", `{"file":`+opstest.Quote(path)+`}`)
	want := "✓ msn_ID Invite flow (created)\n" +
		"  tsk_ID Backend API                   timebox 2h\n" +
		"  tsk_ID Invite screen                 after backend:merged  gate required\n" +
		"  tsk_ID Docs                          timebox 1h30m  after backend,ui\n"
	if got := opstest.IDPattern(opstest.Text(first)); got != want {
		t.Fatalf("apply\n got %q\nwant %q", got, want)
	}
	second := h.Must("mission.apply", `{"file":`+opstest.Quote(path)+`}`)
	if got := opstest.IDPattern(opstest.Text(second)); got != strings.Replace(want, "(created)", "(found)", 1) {
		t.Fatalf("re-run\n got %q", got)
	}
	var a, b struct {
		MissionID string `json:"mission_id"`
		Created   bool   `json:"created"`
		Tasks     []struct {
			Key, ID string
			Created bool
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(h.JSON(first)), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(h.JSON(second)), &b); err != nil {
		t.Fatal(err)
	}
	if !a.Created || b.Created || a.MissionID != b.MissionID || len(b.Tasks) != 3 {
		t.Fatalf("idempotence: %s / %s", h.JSON(first), h.JSON(second))
	}
	for i := range b.Tasks {
		if b.Tasks[i].Created || b.Tasks[i].ID != a.Tasks[i].ID {
			t.Fatalf("re-run created task %s", b.Tasks[i].Key)
		}
	}
	did, err := ids.ParseAny(a.Tasks[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := h.App.Get(t.Context(), engine.Ref{Table: schema.TableTasks, ID: did})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := value.JSONForm(r.Fields["blocked_by"])
	docs := string(raw)
	for _, up := range []string{a.Tasks[0].ID + `","until":"done`, a.Tasks[1].ID + `","until":"done`} {
		if !strings.Contains(docs, up) {
			t.Fatalf("docs task lacks the edge to %s: %s", up, docs)
		}
	}
	if got := opstest.Text(h.Must("mission.list", ``)); !strings.Contains(got, "(3 tasks)") {
		t.Fatalf("list after apply %q", got)
	}
}

// TestMissionApplyRefusals: the file checks (title, key + title per task,
// duplicate keys, gate other than required, after: of a later or unknown
// key, unreadable file, bad YAML) refuse before anything is written.
func TestMissionApplyRefusals(t *testing.T) {
	h := opstest.New(t)
	cases := map[string]string{
		"no title":     "mission:\n  key: k\ntasks: []\n",
		"no task key":  "mission:\n  title: m\ntasks:\n  - title: t\n",
		"dup key":      "mission:\n  title: m\ntasks:\n  - {key: a, title: t}\n  - {key: a, title: u}\n",
		"gate":         "mission:\n  title: m\ntasks:\n  - {key: a, title: t, gate: optional}\n",
		"after later":  "mission:\n  title: m\ntasks:\n  - {key: a, title: t, after: [b]}\n  - {key: b, title: u}\n",
		"after suffix": "mission:\n  title: m\ntasks:\n  - {key: a, title: t}\n  - {key: b, title: u, after: [\"a:landed\"]}\n",
		"bad yaml":     "mission: [\n",
	}
	dir := t.TempDir()
	for name, body := range cases {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Run("mission.apply", `{"file":`+opstest.Quote(path)+`}`); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: %v, want E_INVALID_INPUT", name, err)
		}
	}
	h.Fails("mission.apply", `{"file":`+opstest.Quote(filepath.Join(dir, "missing.yaml"))+`}`, errs.InvalidInput)
	if got := opstest.Text(h.Must("mission.list", `{"all":true}`)); strings.Contains(got, "] m ") {
		t.Fatalf("a refused file wrote a mission: %q", got)
	}
}

// sameLines compares two outputs as sets of lines (list order of rows
// created in the same millisecond is the id order, not the add order).
func sameLines(a, b string) bool {
	la, lb := strings.Split(strings.TrimSpace(a), "\n"), strings.Split(strings.TrimSpace(b), "\n")
	if len(la) != len(lb) {
		return false
	}
	seen := map[string]int{}
	for _, l := range la {
		seen[l]++
	}
	for _, l := range lb {
		seen[l]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
