package git

import (
	"context"
	"github.com/thesatellite-ai/lore/logic/actorsource"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// TestSubjectNamesWhatThePersonDid pins B2-4: a `task add` commit that
// also carries the person's actor copy and the task list is titled after
// the task ("lore: task add tsk_…", D-569: the id always; D-685: never
// the title), never after the actor row.
func TestSubjectNamesWhatThePersonDid(t *testing.T) {
	t.Parallel()
	g := newGen(t)
	mk := func(table row.Table, p ids.Prefix, field, text string) store.Change {
		t.Helper()
		id, err := ids.Natural(p, text)
		if err != nil {
			t.Fatal(err)
		}
		v, err := value.TextOf(text)
		if err != nil {
			t.Fatal(err)
		}
		stamp := g.clock.Now(hlc.HLC{})
		r, err := store.FromRow(row.Row{Table: table, ID: id, HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{field: v}})
		if err != nil {
			t.Fatal(err)
		}
		return store.Change{Row: r}
	}
	actor := mk("actors", ids.PrefixActor, "name", "Mira")
	list := mk("task_lists", ids.PrefixKind, "name", "Inbox")
	task := mk("tasks", ids.PrefixTask, "title", "Ship the beta\nwith notes")
	if got, want := subject("task.add", []store.Change{actor, list, task}), "lore: task add "+task.Row.ID.String()+" (2 rows + actor "+actor.Row.ID.String()+")"; got != want {
		t.Fatalf("task add subject = %q, want %q", got, want)
	}
	if got := subject(DefaultOp, []store.Change{actor, task}); got != "lore: task save "+task.Row.ID.String()+" (2 rows)" {
		t.Fatalf("plain write subject = %q", got)
	}
	// W-11: writes lore makes by itself say what they are, never "write"
	mch := mk("machines", ids.PrefixMachine, "hostname", "box-1")
	if got := subject(DefaultOp, []store.Change{mch}); got != "lore: machine register "+mch.Row.ID.String() {
		t.Fatalf("machine row subject = %q", got)
	}
	// a refresh of an actor copy (devco:D-665: a new actor row reads
	// "actor register", TestSubjectSideRows)
	refresh := actor
	refresh.Expect = g.clock.Now(hlc.HLC{})
	if got := subject(DefaultOp, []store.Change{refresh}); got != "lore: actor copy "+actor.Row.ID.String() {
		t.Fatalf("actor copy subject = %q", got)
	}
	if got := subject(DefaultOp, []store.Change{list}); got != "lore: task list save "+list.Row.ID.String() {
		t.Fatalf("task list subject = %q", got)
	}
}

// TestSubjectNeverCarriesFreeText pins B3-23: a body (memory, rule) never
// goes into a subject, a delete names only the operation and the id, and
// a title the secret scanner flags falls back to the id.
func TestSubjectNeverCarriesFreeText(t *testing.T) {
	t.Parallel()
	g := newGen(t)
	mk := func(table row.Table, p ids.Prefix, field, text string) store.Change {
		t.Helper()
		id, err := ids.Natural(p, text)
		if err != nil {
			t.Fatal(err)
		}
		v, err := value.TextOf(text)
		if err != nil {
			t.Fatal(err)
		}
		stamp := g.clock.Now(hlc.HLC{})
		r, err := store.FromRow(row.Row{Table: table, ID: id, HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{field: v}})
		if err != nil {
			t.Fatal(err)
		}
		return store.Change{Row: r}
	}
	mem := mk("memories", ids.PrefixMemory, "body", "Client X contract rate is ninety per hour")
	if got, want := subject("memory.add", []store.Change{mem}), "lore: memory add "+mem.Row.ID.String(); got != want {
		t.Fatalf("memory add subject = %q, want %q", got, want)
	}
	rule := mk("rules", ids.PrefixRule, "body", "Run the tests")
	if got := subject("rule.add", []store.Change{rule}); strings.Contains(got, "Run the tests") {
		t.Fatalf("rule body in the subject: %q", got)
	}
	task := mk("tasks", ids.PrefixTask, "title", "Ship the beta")
	for _, op := range []string{"task.delete", "sync.purge", "task.trash", "task.archive"} {
		if got, want := subject(op, []store.Change{task}), "lore: "+strings.ReplaceAll(op, ".", " ")+" "+task.Row.ID.String(); got != want {
			t.Fatalf("%s subject = %q, want %q", op, got, want)
		}
	}
	// a title holding a key-looking value (built at run time, never a real key)
	secret := mk("tasks", ids.PrefixTask, "title", "rotate "+"ghp_"+strings.Repeat("aB3d", 9))
	if got, want := subject("task.add", []store.Change{secret}), "lore: task add "+secret.Row.ID.String(); got != want {
		t.Fatalf("secret title subject = %q, want %q", got, want)
	}
}

// TestSubjectActorOnlyBatch pins devco:D-642: a member's first write
// registered their agent actor in the org store while the command's row
// went elsewhere, and that commit read "lore: memory add act_…" although
// it held no memory. A batch of only actor rows says what it is: register
// for a new row (with the display name), copy for a refresh; a batch that
// holds the command's own row keeps the command's words.
func TestSubjectActorOnlyBatch(t *testing.T) {
	t.Parallel()
	g := newGen(t)
	mk := func(table row.Table, p ids.Prefix, field, text string) store.Change {
		t.Helper()
		id, err := ids.Natural(p, text)
		if err != nil {
			t.Fatal(err)
		}
		v, err := value.TextOf(text)
		if err != nil {
			t.Fatal(err)
		}
		stamp := g.clock.Now(hlc.HLC{})
		r, err := store.FromRow(row.Row{Table: table, ID: id, HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{field: v}})
		if err != nil {
			t.Fatal(err)
		}
		return store.Change{Row: r}
	}
	agent := mk("actors", ids.PrefixActor, "display_name", "Lee Park via claude_code")
	id := agent.Row.ID.String()
	if got, want := subject("memory.add", []store.Change{agent}), "lore: actor register "+id; got != want {
		t.Fatalf("actor-only memory add subject = %q, want %q", got, want)
	}
	refresh := agent
	refresh.Expect = g.clock.Now(hlc.HLC{})
	if got, want := subject("memory.add", []store.Change{refresh}), "lore: actor copy "+id; got != want {
		t.Fatalf("actor refresh subject = %q, want %q", got, want)
	}
	mem := mk("memories", ids.PrefixMemory, "body", "a note")
	if got, want := subject("memory.add", []store.Change{agent, mem}), "lore: memory add "+mem.Row.ID.String()+" (+ actor "+id+")"; got != want {
		t.Fatalf("memory add with actor copy subject = %q, want %q", got, want)
	}
}

// TestSubjectSideRows (devco:D-665): a commit's subject names only rows it
// holds and names every row written on the side of the command: the
// writer's new actor row, a folder's project row. Before, a memory with
// the member's new actor row read "(2 rows)", a join's batch "(5 rows)",
// the folder's project row made by a rule add "lore: rule add prj_…", and
// lore's own registration of an agent "lore: actor copy act_…".
func TestSubjectSideRows(t *testing.T) {
	t.Parallel()
	g := newGen(t)
	mk := func(table row.Table, p ids.Prefix, field, text string) store.Change {
		t.Helper()
		id, err := ids.Natural(p, text)
		if err != nil {
			t.Fatal(err)
		}
		v, err := value.TextOf(text)
		if err != nil {
			t.Fatal(err)
		}
		stamp := g.clock.Now(hlc.HLC{})
		r, err := store.FromRow(row.Row{Table: table, ID: id, HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{field: v}})
		if err != nil {
			t.Fatal(err)
		}
		return store.Change{Row: r}
	}
	agent := mk("actors", ids.PrefixActor, "display_name", "Lee Park via claude_code")
	other := mk("actors", ids.PrefixActor, "display_name", "Kim Roe")
	prj := mk("projects", ids.PrefixProject, "name", "web")
	mem := mk("memories", ids.PrefixMemory, "body", "a note")
	tsk := mk("tasks", ids.PrefixTask, "title", "Ship the beta")
	tsk2 := mk("tasks", ids.PrefixTask, "title", "Write the notes")
	a, p := agent.Row.ID.String(), prj.Row.ID.String()
	cases := []struct {
		name, op string
		changes  []store.Change
		want     string
	}{
		{"memory with its writer's actor", "memory.add", []store.Change{mem, agent}, "lore: memory add " + mem.Row.ID.String() + " (+ actor " + a + ")"},
		{"tasks with actor and project", "task.add", []store.Change{tsk, tsk2, agent, prj}, "lore: task add " + tsk.Row.ID.String() + " (2 rows + actor " + a + ", project " + p + ")"},
		{"three side rows", "memory.add", []store.Change{mem, agent, other, prj}, "lore: memory add " + mem.Row.ID.String() + " (+ actor " + a + ", actor " + other.Row.ID.String() + ", 1 more)"},
		{"folder project row", "project.add", []store.Change{prj}, "lore: project add " + p},
		{"actor merge keeps its actors", "actor.merge", []store.Change{agent, other}, "lore: actor merge " + a + " (2 rows)"},
		{"new actor outside a command", DefaultOp, []store.Change{agent}, "lore: actor register " + a},
	}
	for _, c := range cases {
		if got := subject(c.op, c.changes); got != c.want {
			t.Errorf("%s: subject = %q, want %q", c.name, got, c.want)
		}
	}
	refresh := agent
	refresh.Expect = g.clock.Now(hlc.HLC{})
	if got, want := subject(DefaultOp, []store.Change{refresh}), "lore: actor copy "+a; got != want {
		t.Errorf("refresh outside a command: subject = %q, want %q (W-11)", got, want)
	}
	ctx := store.WithCommitTitle(context.Background(), "setup join: lee@example.test")
	if got, want := subjectOf(ctx, "setup.join", []store.Change{mem, tsk, agent}), "lore: setup join: lee@example.test (2 rows + actor "+a+")"; got != want {
		t.Errorf("titled join: subject = %q, want %q", got, want)
	}
}

// TestSubjectIDOnlyForEveryKind pins devco D-685 (owner use pass 4 weak 1):
// one subject rule for every kind. Rule and memory subjects were id-only
// (owner decision 3, 2026-11-18) while a task claim still read "lore: task
// claim tsk_…: Question: …"; a title, a list or project name and an
// actor's name are row text that can be private, and a subject outlives a
// delete. Every subject is the command words, the id and counts only.
func TestSubjectIDOnlyForEveryKind(t *testing.T) {
	t.Parallel()
	g := newGen(t)
	mk := func(table row.Table, p ids.Prefix, field, text string) store.Change {
		t.Helper()
		id, err := ids.Natural(p, text)
		if err != nil {
			t.Fatal(err)
		}
		v, err := value.TextOf(text)
		if err != nil {
			t.Fatal(err)
		}
		stamp := g.clock.Now(hlc.HLC{})
		r, err := store.FromRow(row.Row{Table: table, ID: id, HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{field: v}})
		if err != nil {
			t.Fatal(err)
		}
		return store.Change{Row: r}
	}
	const private = "Question: acme-co pricing for the renewal"
	for _, c := range []struct {
		op     string
		change store.Change
	}{
		{"task.claim", mk("tasks", ids.PrefixTask, "title", private)},
		{"task.add", mk("tasks", ids.PrefixTask, "title", private)},
		{"plan.add", mk("plans", ids.PrefixPlan, "title", private)},
		{"project.add", mk(projectsTable, ids.PrefixProject, "name", private)},
		{"memory.add", mk("actors", ids.PrefixActor, actorsource.FieldDisplayName, private)},
		{DefaultOp, mk("actors", ids.PrefixActor, actorsource.FieldDisplayName, private)},
	} {
		got := subject(c.op, []store.Change{c.change})
		if strings.Contains(got, "acme-co") || strings.Contains(got, ": ") && !strings.HasPrefix(got, "lore: ") || strings.Count(got, ":") != 1 {
			t.Errorf("%s subject = %q, want the command words and the id only", c.op, got)
		}
		if !strings.HasSuffix(got, c.change.Row.ID.String()) {
			t.Errorf("%s subject = %q, want it to end with the id %s", c.op, got, c.change.Row.ID)
		}
	}
}
