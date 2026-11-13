package task_test

import (
	"encoding/json"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// The want strings below are v0.1.17's output (lore 0.1.17 run in a
// scratch HOME and repo, 2026-11-04), ids normalised by opstest.IDPattern.
// Deltas against v0.1.17 are named where they are asserted.

// TestTextMatchesV017 drives one session of the human commands and checks
// each Text() against the v0.1.17 lines.
func TestTextMatchesV017(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	check := func(step, got, want string) {
		t.Helper()
		if lines(got) != want {
			t.Errorf("%s:\n got %q\nwant %q", step, lines(got), want)
		}
	}
	res := h.Must(opstask.OpAdd, `{"title":"Fix the login redirect","priority":"high"}`)
	// delta: the first add names the default tasklist it created (B3-8)
	check("add", opstest.Text(res), "✓ tsk_ID — Fix the login redirect [high]\n  created tasklist tlt_ID \"default\" for tasks added without --tasklist\n")
	second := addID(h, h.Must(opstask.OpAdd, `{"title":"Second","body":"the body text","due":"2026-12-01"}`))
	add(h, "Prop one", `"commitment":"proposed"`)
	add(h, "Some one", `"commitment":"someday"`)
	add(h, "Later", `"defer_until":"2099-01-05"`)
	check("add --key", opstest.Text(h.Must(opstask.OpAdd, `{"title":"Keyed","key":"k1"}`)), "✓ tsk_ID — Keyed [medium]\n  on tasklist tlt_ID \"default\" (the default; --tasklist picks another)\n") // delta (U-3): names the default list
	check("add --key again", opstest.Text(h.Must(opstask.OpAdd, `{"title":"Keyed","key":"k1"}`)), "✓ tsk_ID — Keyed (already added with --key k1)\n")

	check("list", opstest.Text(h.Must(opstask.OpList, ``)), // delta (D-11): says what the commitment filter hid
		"○ tsk_ID Fix the login redirect [high]\n○ tsk_ID Second [medium] due:2026-12-01\n○ tsk_ID Keyed [medium]\n2 hidden (1 proposed, 1 someday); --include-proposed / --include-someday show them\n")
	check("list --all", opstest.Text(h.Must(opstask.OpList, `{"all":true}`)),
		"○ tsk_ID Fix the login redirect [high]\n○ tsk_ID Second [medium] due:2026-12-01\n○ tsk_ID Prop one [medium] (proposed)\n"+
			"○ tsk_ID Some one [medium] (someday)\n○ tsk_ID Later [medium] ⏾2099-01-05\n○ tsk_ID Keyed [medium]\n")
	ts := h.Must(opstask.OpShow, `{"id":"`+second+`"}`) // INTEG-AUDIT-2: ends with the writers line
	check("show", opstest.Text(ts),
		"tsk_ID\n  title:    Second\n  status:   todo\n  priority: medium\n  commit:   accepted\n  due:      2026-12-01\n\nthe body text\n"+opstest.IDPattern(contenttest.WriterLine(t, h.JSON(ts), "data")))
	check("triage", opstest.Text(h.Must(opstask.OpTriage, ``)), "○ tsk_ID Prop one [medium] (proposed)\n")
	check("someday", opstest.Text(h.Must(opstask.OpSomeday, ``)), "○ tsk_ID Some one [medium] (someday)\n")
	check("deferred", opstest.Text(h.Must(opstask.OpDeferred, ``)), "○ tsk_ID Later [medium] ⏾2099-01-05\n")
	check("search", opstest.Text(h.Must(opstask.OpSearch, `{"query":"login"}`)), "tsk_ID [todo] Fix the login redirect\n")
	check("search none", opstest.Text(h.Must(opstask.OpSearch, `{"query":"nothinghere"}`)), "(no matches)\n")

	check("start", opstest.Text(h.Must(opstask.OpStart, `{"id":"`+second+`"}`)), "✓ tsk_ID started\n")
	check("done --pr", opstest.Text(h.Must(opstask.OpDone, `{"id":"`+second+`","pr":12}`)),
		"✓ tsk_ID done (pr #12); waiting for merge\n  learned something? `lore memory add --body \"…\"` (or pass --lesson next time)\n")
	third := add(h, "Third", "")
	check("done --commit --lesson", opstest.Text(h.Must(opstask.OpDone, `{"id":"`+third+`","commit":"abcdef1234567","lesson":"learned x"}`)),
		"✓ tsk_ID done (commit abcdef1, not linked to the task; link it with: lore link add --entity tsk_ID --commit abcdef1234567); waiting for merge\n  lesson → mem_ID\n") // A-11: says the commit is not linked
	fourth := add(h, "Fourth", "")
	check("done", opstest.Text(h.Must(opstask.OpDone, `{"id":"`+fourth+`"}`)),
		"✓ tsk_ID done\n  learned something? `lore memory add --body \"…\"` (or pass --lesson next time)\n")
	check("done --no-lesson", opstest.Text(h.Must(opstask.OpDone, `{"id":"`+fourth+`","no_lesson":true}`)), "✓ tsk_ID done\n")
	fifth := add(h, "Fifth", "")
	check("cancel", opstest.Text(h.Must(opstask.OpCancel, `{"id":"`+fifth+`"}`)), "✓ tsk_ID cancelled\n")
	check("edit", opstest.Text(h.Must(opstask.OpEdit, `{"id":"`+fifth+`","priority":"low"}`)), "✓ tsk_ID updated\n")
	// Delta: v0.1.17 printed "deleted (hard)"; DQ8 every delete is soft, so
	// the line names the trash and the restore command.
	check("delete", opstest.Text(h.MustYes(opstask.OpDelete, `{"id":"`+fifth+`"}`)), "✓ tsk_ID moved to the trash (restore: lore trash restore --id tsk_ID)\n")
	check("empty list", opstask.ListOut{}.Text(), "(no tasks)\n")
}

// TestShowDoneByAndJSON: a done task shows "done by" with the actor's
// display name, and show --json keeps v0.1.17's taskFull key order.
func TestShowDoneByAndJSON(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := add(h, "Second", `"body":"b","due":"2026-12-01"`)
	h.Must(opstask.OpStart, `{"id":"`+id+`"}`)
	h.Must(opstask.OpDone, `{"id":"`+id+`"}`)
	show := h.Must(opstask.OpShow, `{"id":"`+id+`"}`)
	if !strings.Contains(opstest.Text(show), "  done by:  ") {
		t.Errorf("no done by line: %q", opstest.Text(show))
	}
	// v0.1.17 task show --json key order (taskFull).
	order := []string{"id", "title", "body", "status", "priority", "commitment", "due_at", "started_at", "completed_at", "tasklist_id", "project_id", "created_at"}
	raw := h.JSON(show)
	at := 0
	for _, k := range order {
		i := strings.Index(raw[at:], `"`+k+`":`)
		if i < 0 {
			t.Fatalf("key %s missing or out of order in %s", k, raw)
		}
		at += i
	}
	if !strings.HasPrefix(raw, `{"schema_version":1,"kind":"task.show","data":{"id":"`) {
		t.Errorf("envelope: %s", raw)
	}
}

// TestNotFoundAndBadIDs: every id-taking op answers a missing task with
// v0.1.17's message and a malformed id with E_INVALID_ID.
func TestNotFoundAndBadIDs(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	gone := add(h, "gone", "")
	h.MustYes(opstask.OpDelete, `{"id":"`+gone+`"}`)
	missing := "tsk_01a1a5aca7f57659be7320c5f4b70011"
	for _, name := range []string{opstask.OpShow, opstask.OpStart, opstask.OpDone, opstask.OpCancel, opstask.OpEdit, opstask.OpDelete} {
		for _, id := range []string{missing, gone} {
			in := `{"id":"` + id + `","title":"x"}`
			if name != opstask.OpEdit {
				in = `{"id":"` + id + `"}`
			}
			_, err := h.RunYes(name, in)
			if id == gone { // DELETION 3.2: a trashed task says where it is
				if !errs.Has(err, errs.InTrash) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "lore trash restore --id "+id) {
					t.Errorf("%s %s: %v, want E_IN_TRASH", name, id, err)
				}
				continue
			}
			if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), `task "`+id+`" not found`) {
				t.Errorf("%s %s: %v", name, id, err)
			}
		}
		for _, bad := range []string{"xyz", "mem_01a1a5aca7f57659be7320c5f4b70011", "tsk_short", "tsk_01A1A5ACA7F57659BE7320C5F4B70011"} {
			if _, err := h.RunYes(name, `{"id":"`+bad+`"}`); !errs.Has(err, errs.InvalidID) {
				t.Errorf("%s %q: %v, want E_INVALID_ID", name, bad, err)
			}
		}
	}
}

// TestAddValidation: closed sets and dates are refused before anything is
// written. Delta: v0.1.17 stored any --priority text ("huge"); the core
// checks the closed set (schema TaskPriorities).
func TestAddValidation(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	cases := []struct {
		name, in string
		code     errs.Code
	}{
		{"empty title", `{"title":"   "}`, errs.InvalidInput},
		{"no title", `{}`, errs.InvalidInput},
		{"bad priority", `{"title":"x","priority":"huge"}`, errs.InvalidInput},
		{"bad commitment", `{"title":"x","commitment":"maybe"}`, errs.InvalidInput},
		{"bad due", `{"title":"x","due":"2026-13-45"}`, errs.Date},
		{"bad defer", `{"title":"x","defer_until":"11/12"}`, errs.Date},
		{"mission wrong prefix", `{"title":"x","mission":"tsk_01a1a5aca7f57659be7320c5f4b70011"}`, errs.InvalidID},
		{"after unknown", `{"title":"x","after":["tsk_01a1a5aca7f57659be7320c5f4b70011"]}`, errs.NotFound},
		{"repo unknown", `{"title":"x","repo":"nope"}`, errs.NotFound},
		{"project unknown", `{"title":"x","project":"nope"}`, errs.NotFound},
		{"ref bad", `{"title":"x","ref":["nope"]}`, errs.NotFound}, // a resource name no resource has (B-ERRS-1: not id-shaped)
		{"ref commit empty", `{"title":"x","ref":["commit:"]}`, errs.InvalidInput},
		{"ref missing row", `{"title":"x","ref":["mem_01a1a5aca7f57659be7320c5f4b70011"]}`, errs.NotFound},
		// P3-COORD-3, P3-COORD-3a: App.Run always supplies the standalone
		// Team; in this client repo its roster is the org's, never the
		// repo's, so a handle is refused, never guessed.
		{"assign no team host", `{"title":"x","assign":["@bob"]}`, errs.LeakBlocked},
		{"assign unknown actor", `{"title":"x","assign":["nobody"]}`, errs.NotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { h.Fails(opstask.OpAdd, c.in, c.code) })
	}
	if got := h.Must(opstask.OpList, `{"all":true}`); h.Field(got, "count") != "" || !strings.Contains(h.JSON(got), `"data":[]`) {
		t.Errorf("a refused add wrote a task: %s", h.JSON(got))
	}
	// Unicode and a huge title round-trip.
	for _, title := range []string{"größer – 日本語 🚀", strings.Repeat("long ", 400)} {
		id := add(h, title, "")
		if _, r := get(h, schema.TableTasks, id); field(r, "title") != strings.TrimSpace(title) {
			t.Errorf("title not kept: %q", field(r, "title"))
		}
	}
}

// TestAddDefaults: a new task is todo / medium / accepted, stamps the
// current actor, joins the project's "default" tasklist (created once per
// store, on demand), and --timebox / --role are stored.
func TestAddDefaults(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, withOrg(t))
	a := add(h, "a", `"timebox":3600000000000,"role":"coordinator"`)
	b := add(h, "b", "")
	_, ra := get(h, schema.TableTasks, a)
	_, rb := get(h, schema.TableTasks, b)
	for k, want := range map[string]string{"status": "todo", "priority": "medium", "commitment": "accepted", "timebox_seconds": "3600", "role": "coordinator", "project_id": h.Project} {
		if got := field(ra, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if field(ra, "tasklist_id") == "" || field(ra, "tasklist_id") != field(rb, "tasklist_id") {
		t.Errorf("default tasklist not shared: %q %q", field(ra, "tasklist_id"), field(rb, "tasklist_id"))
	}
	_, tl := get(h, schema.TableTasklists, field(ra, "tasklist_id"))
	if field(tl, "title") != opstask.DefaultTasklistTitle || field(tl, "status_str") != opstask.DefaultTasklistStatus {
		t.Errorf("default tasklist: %v", tl.Fields)
	}
	// Another store gets its own default list (a ref never crosses
	// stores for it).
	c := addID(h, h.Must(opstask.OpAdd, `{"title":"c","to":"`+orgStore+`"}`))
	src, rc := get(h, schema.TableTasks, c)
	if src != orgStore {
		t.Fatalf("task landed in %s", src)
	}
	tsrc, _ := get(h, schema.TableTasklists, field(rc, "tasklist_id"))
	if tsrc != orgStore || field(rc, "tasklist_id") == field(ra, "tasklist_id") {
		t.Errorf("org task's default list %s in %s", field(rc, "tasklist_id"), tsrc)
	}
	// An explicit --tasklist creates no default list.
	before := h.Must("tasklist.list", ``)
	add(h, "d", `"tasklist":"`+field(ra, "tasklist_id")+`"`)
	if h.JSON(h.Must("tasklist.list", ``)) != h.JSON(before) {
		t.Error("explicit --tasklist created a list")
	}
}

// TestKeyIdempotent: --key is idempotent per project and mission; a
// different key or mission adds a new task.
func TestKeyIdempotent(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	msn := addID(h, h.Must("mission.add", `{"title":"m"}`))
	first := add(h, "k", `"key":"k1"`)
	if again := add(h, "k changed title", `"key":"k1"`); again != first {
		t.Errorf("same key added %s, want %s", again, first)
	}
	if other := add(h, "k", `"key":"k2"`); other == first {
		t.Error("another key reused the task")
	}
	inMission := add(h, "k", `"key":"k1","mission":"`+msn+`"`)
	if inMission == first {
		t.Error("the same key in a mission reused the task outside it")
	}
	if again := add(h, "k", `"key":"k1","mission":"`+msn+`"`); again != inMission {
		t.Error("key in mission not idempotent")
	}
}

// TestAfterModesAndCycles: --after defaults to done in the same repo and
// merged across repos, takes explicit :done / :merged, dedupes, and edit
// refuses a cycle (E_CYCLE) including a self edge.
func TestAfterModesAndCycles(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	repoA := create(h, schema.TableRepos, map[string]value.Value{"project_id": ref(t, h.Project), "mount_name": text(t, "web")})
	create(h, schema.TableRepos, map[string]value.Value{"project_id": ref(t, h.Project), "mount_name": text(t, "api")})
	up := add(h, "up", `"repo":"web"`)
	upOther := add(h, "up other", `"repo":"api"`)
	deps := func(id string) []map[string]string {
		t.Helper()
		_, r := get(h, schema.TableTasks, id)
		var out []map[string]string
		if v, ok := r.Fields["blocked_by"]; ok && v != nil {
			raw, err := v.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("blocked_by %s: %v", raw, err)
			}
		}
		return out
	}
	down := add(h, "down", `"repo":"web","after":["`+up+`","`+upOther+`",`+opstest.Quote(up)+`]`)
	got := deps(down)
	if len(got) != 2 || got[0]["task_id"] != up || got[0]["until"] != "done" || got[1]["until"] != "merged" {
		t.Errorf("default modes: %v", got)
	}
	explicit := add(h, "explicit", `"repo":"web","after":["`+up+`:merged,`+upOther+`:done"]`)
	if got := deps(explicit); len(got) != 2 || got[0]["until"] != "merged" || got[1]["until"] != "done" {
		t.Errorf("explicit modes: %v", got)
	}
	_ = repoA
	h.Fails(opstask.OpEdit, `{"id":"`+up+`","after":["`+down+`"]}`, errs.Cycle)
	h.Fails(opstask.OpEdit, `{"id":"`+up+`","after":["`+up+`"]}`, errs.Cycle)
	// Through a third task: up → mid → down → up.
	mid := add(h, "mid", `"after":["`+down+`"]`)
	msg := h.Fails(opstask.OpEdit, `{"id":"`+up+`","after":["`+mid+`"]}`, errs.Cycle)
	if !strings.Contains(msg, "dependency cycle: "+up+" → "+mid) {
		t.Errorf("cycle path: %s", msg)
	}
	// A legal replacement edge is written.
	h.Must(opstask.OpEdit, `{"id":"`+up+`","after":["`+upOther+`"]}`)
	if got := deps(up); len(got) != 1 || got[0]["task_id"] != upOther {
		t.Errorf("edit --after: %v", got)
	}
}

// TestListFilters walks the list filter matrix.
func TestListFilters(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, withOrg(t))
	actor := newActor(h, "Bob")
	todo := add(h, "todo", "")
	prog := add(h, "prog", "")
	h.Must(opstask.OpStart, `{"id":"`+prog+`"}`)
	done := add(h, "done", "")
	h.Must(opstask.OpDone, `{"id":"`+done+`","no_lesson":true}`)
	canc := add(h, "canc", "")
	h.Must(opstask.OpCancel, `{"id":"`+canc+`"}`)
	prop := add(h, "prop", `"commitment":"proposed"`)
	some := add(h, "some", `"commitment":"someday"`)
	def := add(h, "def", `"defer_until":"2099-01-01"`)
	past := add(h, "past", `"defer_until":"2000-01-01"`)
	assigned := add(h, "assigned", `"assigned_to":"`+actor+`"`)
	org := addID(h, h.Must(opstask.OpAdd, `{"title":"org","to":"`+orgStore+`"}`))

	ids := func(in string) []string {
		t.Helper()
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpList, in))), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range out.Data {
			got = append(got, d.ID)
		}
		return got
	}
	all := []string{todo, prog, done, canc, prop, some, def, past, assigned, org}
	cases := []struct {
		name, in string
		want     []string
	}{
		{"default", `{}`, []string{todo, prog, past, assigned, org}},
		{"all", `{"all":true}`, all},
		{"status done", `{"status":"done"}`, []string{done}},
		{"status cancelled", `{"status":"cancelled"}`, []string{canc}},
		{"status in_progress", `{"status":"in_progress"}`, []string{prog}},
		{"commitment proposed", `{"commitment":"proposed"}`, []string{prop}},
		{"commitment someday", `{"commitment":"someday"}`, []string{some}},
		{"include proposed", `{"include_proposed":true}`, []string{todo, prog, prop, past, assigned, org}},
		{"include someday", `{"include_someday":true}`, []string{todo, prog, some, past, assigned, org}},
		{"include deferred", `{"include_deferred":true}`, []string{todo, prog, def, past, assigned, org}},
		{"assignee id", `{"assignee":"` + actor + `"}`, []string{assigned}},
		{"assignee display name any case", `{"assignee":"bob"}`, []string{assigned}},
		{"assignee stable key", `{"assignee":"human:bob"}`, []string{assigned}},
		{"store org", `{"store":"` + orgStore + `"}`, []string{org}},
		{"store repo", `{"store":"repo"}`, []string{todo, prog, past, assigned}},
		{"project by name", `{"project":"` + opstest.ProjectName + `","store":"repo"}`, []string{todo, prog, past, assigned}},
		{"all + status", `{"all":true,"status":"todo"}`, []string{todo, prop, some, def, past, assigned, org}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ids(c.in)
			if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(c.want), ",") {
				t.Errorf("got %v\nwant %v", got, c.want)
			}
		})
	}
	// The org store's task carries its store label (human) and _source
	// (FLAG1: the object naming the store).
	listed := h.Must(opstask.OpList, `{"store":"`+orgStore+`"}`)
	if !strings.HasSuffix(opstest.Text(listed), "  ["+orgStore+"]\n") || !strings.Contains(h.JSON(listed), `"_source":{"store":"`+orgStore+`"`) {
		t.Errorf("store label: %q %s", opstest.Text(listed), h.JSON(listed))
	}
	for _, bad := range []string{`{"commitment":"maybe"}`, `{"assignee":"nobody"}`, `{"assignee":"  "}`, `{"project":"nope"}`} {
		if _, err := h.Run(opstask.OpList, bad); err == nil {
			t.Errorf("list %s accepted", bad)
		}
	}
}

// TestSearchFilters: search keeps active tasks unless --all, applies the
// repo scope and --limit after the filters, and loads relations (JSON
// null when absent, keys in v0.1.17's order).
func TestSearchFilters(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	create(h, schema.TableRepos, map[string]value.Value{"project_id": ref(t, h.Project), "mount_name": text(t, "web")})
	master := add(h, "alpha master", "")
	inRepo := add(h, "alpha web", `"repo":"web"`)
	doneT := add(h, "alpha done", "")
	h.Must(opstask.OpDone, `{"id":"`+doneT+`","no_lesson":true}`)
	add(h, "alpha proposed", `"commitment":"proposed"`)
	for i := 0; i < 3; i++ {
		add(h, "alpha extra", "")
	}
	count := func(in string) int {
		t.Helper()
		var out struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpSearch, in))), &out); err != nil {
			t.Fatal(err)
		}
		return out.Count
	}
	cases := map[string]int{
		`{"query":"alpha"}`:                                4, // master + 3 extra (active, master scope)
		`{"query":"alpha","all":true}`:                     6, // + done + proposed
		`{"query":"alpha","repo":"web"}`:                   5, // + the web task
		`{"query":"alpha","repo":"web","no_inherit":true}`: 1,
		`{"query":"alpha","all_repos":true}`:               5,
		`{"query":"alpha","master_only":true}`:             4,
		`{"query":"alpha","limit":2}`:                      2,
		`{"query":"alpha","all":true,"all_repos":true}`:    7,
	}
	for in, want := range cases {
		if got := count(in); got != want {
			t.Errorf("%s: %d hits, want %d", in, got, want)
		}
	}
	_, _ = master, inRepo
	for _, bad := range []string{`{"query":"  "}`, `{"query":"alpha","limit":-1}`, `{"query":"alpha","limit":1001}`, `{"query":"alpha","repo":"nope"}`} {
		if _, err := h.Run(opstask.OpSearch, bad); err == nil {
			t.Errorf("search %s accepted", bad)
		}
	}
	raw := h.JSON(h.Must(opstask.OpSearch, `{"query":"master"}`))
	if !strings.Contains(raw, `"relations":{"mission":null,"plan":null,"tasklist":{"id":"tlt_`) {
		t.Errorf("relations (v0.1.17 order mission, plan, tasklist; null when absent): %s", raw)
	}
}

// TestEditMatrix: each flag patches its field, --clear-* remove one, and
// an empty edit is refused. Delta: v0.1.17 printed "updated" for an
// empty edit; the core refuses it (nothing to change).
func TestEditMatrix(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	actor := newActor(h, "Ann")
	msn := addID(h, h.Must("mission.add", `{"title":"m"}`))
	id := add(h, "t", `"due":"2026-01-01","defer_until":"2099-01-01"`)
	steps := []struct {
		in    string
		field string
		want  string
	}{
		{`"title":"  new title  "`, "title", "new title"},
		{`"body":"nb"`, "body", "nb"},
		{`"priority":"urgent"`, "priority", "urgent"},
		{`"status":"blocked"`, "status", "blocked"},
		{`"commitment":"someday"`, "commitment", "someday"},
		{`"role":"reviewer"`, "role", "reviewer"},
		{`"timebox":120000000000`, "timebox_seconds", "120"},
		{`"assigned_to":"` + actor + `"`, "assigned_to_actor_id", actor},
		{`"clear_assignee":true`, "assigned_to_actor_id", ""},
		{`"clear_defer":true`, "deferred_until", ""},
		{`"clear_due":true`, "due_at", ""},
		{`"mission":"` + msn + `"`, "mission_id", msn},
		{`"clear_mission":true`, "mission_id", ""},
	}
	for _, s := range steps {
		h.Must(opstask.OpEdit, `{"id":"`+id+`",`+s.in+`}`)
		if _, r := get(h, schema.TableTasks, id); field(r, s.field) != s.want {
			t.Errorf("edit %s: %s = %q, want %q", s.in, s.field, field(r, s.field), s.want)
		}
	}
	h.Fails(opstask.OpEdit, `{"id":"`+id+`"}`, errs.InvalidInput)
	for _, bad := range []string{`"priority":"huge"`, `"status":"open"`, `"commitment":"x"`, `"due":"2026-02-30"`, `"plan":"tsk_01a1a5aca7f57659be7320c5f4b70011"`} {
		if _, err := h.Run(opstask.OpEdit, `{"id":"`+id+`",`+bad+`}`); !errs.Has(err, errs.InvalidInput) && !errs.Has(err, errs.InvalidID) && !errs.Has(err, errs.Date) {
			t.Errorf("edit %s: %v", bad, err)
		}
	}
}

// TestStartDoneStamps: start sets in_progress + started_at, accepts and
// un-defers; done stamps completed_at, done_commit, done_pr, done_by and
// the lesson memory lands in the task's store pointing at the task.
func TestStartDoneStamps(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, withOrg(t))
	id := add(h, "t", `"commitment":"someday","defer_until":"2099-01-01"`)
	h.Must(opstask.OpStart, `{"id":"`+id+`"}`)
	_, r := get(h, schema.TableTasks, id)
	if field(r, "status") != "in_progress" || field(r, "started_at") == "" || field(r, "commitment") != "accepted" || field(r, "deferred_until") != "" {
		t.Errorf("start: %v", r.Fields)
	}
	h.Fails(opstask.OpDone, `{"id":"`+id+`","pr":-1}`, errs.InvalidInput)
	h.Must(opstask.OpDone, `{"id":"`+id+`","pr":7,"commit":" abc123 "}`)
	_, r = get(h, schema.TableTasks, id)
	if field(r, "status") != "done" || field(r, "completed_at") == "" || field(r, "done_pr") != "7" || field(r, "done_commit") != "abc123" || field(r, "done_by") != opstest.Actor {
		t.Errorf("done: %v", r.Fields)
	}
	org := addID(h, h.Must(opstask.OpAdd, `{"title":"o","to":"`+orgStore+`"}`))
	res := h.Must(opstask.OpDone, `{"id":"`+org+`","lesson":"  tokens expire  "}`)
	mem := h.Field(res, "lesson_id")
	src, m := get(h, schema.TableMemories, mem)
	if src != orgStore || field(m, "body") != "tokens expire" || field(m, "source_kind") != "lesson" || field(m, "source_ref") != org || field(m, "kind") != schema.MemoryKindRetrieved {
		t.Errorf("lesson in %s: %v", src, m.Fields)
	}
}

// sortedCopy returns a sorted copy of s.
func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
