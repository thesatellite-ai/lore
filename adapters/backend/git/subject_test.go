package git

import (
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
// the task ("lore: task add: <title>"), never after the actor row.
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
	if got, want := subject("task.add", []store.Change{actor, list, task}), "lore: task add: Ship the beta (3 rows)"; got != want {
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
	if got := subject(DefaultOp, []store.Change{actor}); got != "lore: actor copy "+actor.Row.ID.String() {
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
