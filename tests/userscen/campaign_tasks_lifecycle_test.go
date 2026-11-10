//go:build userscen

package userscen

// The reverse path of every row kind of the tasks area, the way a person
// walks it: add → show → edit → list → search → delete (a --dry-run first
// where offered, and the real delete refused without --yes where it asks)
// → list (gone) → trash restore → show (back, same values) → delete →
// trash purge → show (gone for good). Every --json document is checked
// against docs/reference/openapi.json for its operation. Odd values
// (unicode, emoji, RTL, 10k characters, reserved words, slashes) round-trip
// through add → show unchanged (outer whitespace is trimmed, never inner).

import (
	"encoding/json"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// campTasksNoun is one row kind of the area and how a person drives it.
type campTasksNoun struct {
	noun   string                  // the CLI noun (task, mission, …)
	opPfx  string                  // the operationId prefix (task., mission., …)
	field  string                  // the show field holding the title
	add    func(v string) []string // the add arguments for title v
	edit   func(v string) []string // the edit flags setting title v
	noShow bool                    // reminder: read through list, no show verb
	search bool                    // the noun has a search verb
	dryRun bool                    // delete takes --dry-run
	asks   bool                    // delete without --yes is E_NEEDS_CONFIRM
	// keepsPad: the content kinds keep a title's outer blanks byte for
	// byte (ops/internal/contenttest "padded", v0.1.17 parity); the work
	// kinds (task, mission, plan, tasklist) trim them.
	keepsPad bool
}

// campTasksNouns is every row kind with a full lifecycle in the area.
var campTasksNouns = []campTasksNoun{
	{noun: "task", opPfx: "task.", field: "title", search: true, asks: false,
		add:  func(v string) []string { return []string{"task", "add", v} },
		edit: func(v string) []string { return []string{"--title", v} }},
	{noun: "mission", opPfx: "mission.", field: "title", search: true, dryRun: true, asks: true,
		add:  func(v string) []string { return []string{"mission", "add", v} },
		edit: func(v string) []string { return []string{"--title", v} }},
	{noun: "plan", opPfx: "plan.", field: "title", search: true, dryRun: true, asks: true,
		add:  func(v string) []string { return []string{"plan", "add", "--title", v, "--body", "the plan"} },
		edit: func(v string) []string { return []string{"--title", v} }},
	{noun: "tasklist", opPfx: "tasklist.", field: "title", search: true, dryRun: true, asks: true,
		add:  func(v string) []string { return []string{"tasklist", "add", "--title", v, "--body", "a list"} },
		edit: func(v string) []string { return []string{"--title", v} }},
	{noun: "workflow", opPfx: "workflow.", field: "name", search: true, asks: true, keepsPad: true,
		add: func(v string) []string {
			return []string{"workflow", "add", "--title", v, "--body", "1. branch 2. test"}
		},
		edit: func(v string) []string { return []string{"--name", v} }},
	{noun: "reminder", opPfx: "reminder.", field: "message", noShow: true, asks: true,
		add: func(v string) []string { return []string{"reminder", "add", v, "--due", "2026-12-01"} }},
}

// campTasksRead returns the row id's title field: show for kinds with a
// show verb, else the row of the list.
func campTasksRead(t *testing.T, c *th.Clone, n campTasksNoun, id string) (string, bool) {
	t.Helper()
	if n.noShow {
		for _, r := range campTasksListRows(t, c, n) {
			if campTasksStr(r, "id") == id {
				return campTasksStr(r, n.field), true
			}
		}
		return "", false
	}
	r := c.Lore(n.noun, "show", id, "--json")
	if r.Code != 0 {
		return "", false
	}
	campTasksCheckShape(t, n.opPfx+"show", r.Stdout)
	return campTasksStr(campTasksData(t, r), n.field), true
}

// campTasksListRows is `lore <noun> list --json`'s rows (shape-checked).
func campTasksListRows(t *testing.T, c *th.Clone, n campTasksNoun) []map[string]json.RawMessage {
	t.Helper()
	args := []string{n.noun, "list", "--json"}
	if n.noun == "task" {
		args = append(args, "--all")
	}
	r := c.Lore(args...).OK(t)
	campTasksCheckShape(t, n.opPfx+"list", r.Stdout)
	env := th.DecodeJSON[tasksListEnvelope[map[string]json.RawMessage]](t, r)
	return env.Data
}

// campTasksListed reports whether id is in the noun's list.
func campTasksListed(t *testing.T, c *th.Clone, n campTasksNoun, id string) bool {
	t.Helper()
	for _, r := range campTasksListRows(t, c, n) {
		if campTasksStr(r, "id") == id {
			return true
		}
	}
	return false
}

// TestCampaignTasks_ReversePath walks every kind's whole life.
func TestCampaignTasks_ReversePath(t *testing.T) {
	for _, n := range campTasksNouns {
		t.Run(n.noun, func(t *testing.T) {
			s := newTasksSolo(t)
			c := s.c
			r := c.Lore(append(n.add("Ship the invite flow"), "--json")...).OK(t)
			campTasksCheckShape(t, n.opPfx+"add", r.Stdout)
			id := tasksID(t, r)
			if got, ok := campTasksRead(t, c, n, id); !ok || got != "Ship the invite flow" {
				t.Fatalf("show after add: %q (found %v)", got, ok)
			}
			if !campTasksListed(t, c, n, id) {
				t.Fatal("list after add does not have the row")
			}
			if n.edit != nil {
				r = c.Lore(append(append([]string{n.noun, "edit", id}, n.edit("Ship the invite flow v2")...), "--json")...).OK(t)
				campTasksCheckShape(t, n.opPfx+"edit", r.Stdout)
				if got, _ := campTasksRead(t, c, n, id); got != "Ship the invite flow v2" {
					t.Fatalf("show after edit: %q", got)
				}
				// The same edit again changes nothing and still succeeds.
				c.MustLore(append([]string{n.noun, "edit", id}, n.edit("Ship the invite flow v2")...)...)
			}
			if n.search {
				r = c.Lore(n.noun, "search", "invite", "--json").OK(t)
				campTasksCheckShape(t, n.opPfx+"search", r.Stdout)
				r.Says(t, id)
			}
			c.MustLore("task", "list") // settle any first-read fetch before hashing
			before := campTasksTreeHash(t, c.Dir)
			if n.dryRun {
				// --dry-run previews a bulk delete (--where); naming the row
				// by id previews exactly it.
				c.MustLore(n.noun, "delete", "--where", "id="+id, "--dry-run").Says(t, id)
				if after := campTasksTreeHash(t, c.Dir); after != before {
					t.Fatal("delete --dry-run changed the clone")
				}
			}
			if n.asks {
				e := campTasksErr(t, c.Lore(n.noun, "delete", id, "--json"))
				if e.Error.Code != "E_NEEDS_CONFIRM" || e.Error.Hint == "" {
					t.Fatalf("delete without --yes: %+v", e.Error)
				}
				if after := campTasksTreeHash(t, c.Dir); after != before {
					t.Fatal("a delete that asked first changed the clone")
				}
			}
			r = c.Lore(n.noun, "delete", id, "--yes", "--json").OK(t)
			campTasksCheckShape(t, n.opPfx+"delete", r.Stdout)
			if campTasksListed(t, c, n, id) {
				t.Fatal("list after delete still has the row")
			}
			if _, ok := campTasksRead(t, c, n, id); ok && !n.noShow {
				t.Fatal("show after delete still answers")
			}
			// Deleting it again is a clear refusal (it is in the trash).
			again := campTasksErr(t, c.Lore(n.noun, "delete", id, "--yes", "--json"))
			if again.Error.Hint == "" {
				t.Errorf("second delete: no hint: %+v", again.Error)
			}
			c.MustLore("trash", "restore", id).Says(t, "restored")
			if got, ok := campTasksRead(t, c, n, id); !ok || (n.edit != nil && got != "Ship the invite flow v2") {
				t.Fatalf("show after restore: %q (found %v)", got, ok)
			}
			if !campTasksListed(t, c, n, id) {
				t.Fatal("list after restore does not have the row")
			}
			c.MustLore(n.noun, "delete", id, "--yes")
			c.MustLore("trash", "purge", id, "--confirm")
			if _, ok := campTasksRead(t, c, n, id); ok {
				t.Fatal("show after purge still answers")
			}
			gone := campTasksErr(t, c.Lore("trash", "restore", id, "--json"))
			if gone.Error.Hint == "" && gone.Error.Code != "E_PURGED" {
				t.Errorf("restore after purge: %+v", gone.Error)
			}
		})
	}
}

// TestCampaignTasks_OddValues: every kind keeps an odd title exactly.
func TestCampaignTasks_OddValues(t *testing.T) {
	values := append(append([]struct{ name, value string }(nil), campTasksOdd...), struct{ name, value string }{"10k characters", campTasksLong})
	for _, n := range campTasksNouns {
		t.Run(n.noun, func(t *testing.T) {
			campTasksOddNoun(t, n, values)
		})
	}
}

// campTasksOddNoun runs the odd-value matrix of one kind in its own repo
// (the kinds run in parallel).
func campTasksOddNoun(t *testing.T, n campTasksNoun, values []struct{ name, value string }) {
	s := newTasksSolo(t)
	{
		for _, v := range values {
			r := s.c.Lore(append(n.add(v.value), "--json")...)
			if r.Code != 0 {
				t.Errorf("%s add %s: %s", n.noun, v.name, r)
				continue
			}
			id := tasksID(t, r)
			got, ok := campTasksRead(t, s.c, n, id)
			want := strings.TrimSpace(v.value)
			if n.keepsPad {
				want = v.value
			}
			if !ok || got != want {
				t.Errorf("%s add %s: show has %q, want %q", n.noun, v.name, campTasksCut(got), campTasksCut(want))
			}
		}
		// An empty title is refused with a hint, never stored.
		e := campTasksErr(t, s.c.Lore(append(n.add(""), "--json")...))
		if e.Error.Hint == "" || (e.Error.Code != "E_INVALID_INPUT" && e.Error.Code != "E_EMPTY_BODY") {
			t.Errorf("%s add with an empty title: %+v", n.noun, e.Error)
		}
	}
}

// campTasksCut shortens a value for a failure message.
func campTasksCut(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
