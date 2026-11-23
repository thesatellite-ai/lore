package wire

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// rowKindVerbs is the lifecycle every searchable row kind exposes (devco
// D-924, from an owner's 440-row import: `lore cookbookrecipe archive`
// did not exist while rules, memories and playbooks archived). A row kind
// is a noun with both `<noun>.add` and `<noun>.search` in the registry.
var rowKindVerbs = []string{"add", "list", "show", "edit", "search", "delete", "archive", "unarchive"}

// rowKindVerbExceptions are the verbs a row kind leaves out on purpose,
// each with why. A new row kind lands with the whole lifecycle or with a
// line here; a verb that is added later must leave this list.
var rowKindVerbExceptions = map[string]map[string]string{
	// Work items end through their status, not an archive stamp: done,
	// cancel, someday and the trash already take them out of the lists.
	"task":     {"archive": "a task ends with done, cancel or someday", "unarchive": "a task ends with done, cancel or someday"},
	"mission":  {"archive": "a mission ends with done or pause", "unarchive": "a mission ends with done or pause"},
	"plan":     {"archive": "a plan is closed through its status (plan edit --status)", "unarchive": "a plan is closed through its status (plan edit --status)"},
	"tasklist": {"archive": "a task list is a view over its tasks; it is deleted when it is no longer wanted", "unarchive": "a task list has no archive"},
	// A handoff is a message to one person: they ack it, the sender never rewrites it.
	"handoff": {"edit": "a handoff is a sent message; send a new one", "archive": "a handoff is closed by `lore handoff ack`", "unarchive": "a handoff is closed by `lore handoff ack`"},
	// A comment is a line in another row's thread: it shows with that row.
	"comment": {"show": "a comment shows in its row's thread (lore comment list --on-id <id>)", "edit": "a comment is a thread entry; delete it and add a new one", "archive": "a comment leaves its thread through delete", "unarchive": "a comment has no archive"},
	// A snapshot is a point-in-time capture; changing it would rewrite what was true then.
	"snapshot": {"edit": "a snapshot records what was true when it was taken; add a new one"},
}

// TestEveryRowKindHasTheLifecycleVerbs walks the operation registry: every
// row kind has each verb of rowKindVerbs unless rowKindVerbExceptions says
// why it cannot (devco D-924).
func TestEveryRowKindHasTheLifecycleVerbs(t *testing.T) {
	t.Parallel()
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	verbs := map[string][]string{}
	for _, e := range a.Registry().Entries() {
		noun, verb, ok := strings.Cut(e.Spec().Name, ".")
		if ok {
			verbs[noun] = append(verbs[noun], verb)
		}
	}
	kinds := 0
	for noun, have := range verbs {
		if !slices.Contains(have, "add") || !slices.Contains(have, "search") {
			continue
		}
		kinds++
		for _, v := range rowKindVerbs {
			why, exempt := rowKindVerbExceptions[noun][v]
			switch has := slices.Contains(have, v); {
			case has && exempt:
				t.Errorf("%s.%s exists now: take it out of rowKindVerbExceptions (%q)", noun, v, why)
			case !has && !exempt:
				t.Errorf("row kind %s has no %s: add `lore %s %s` (content.Ops has the step), or say in rowKindVerbExceptions why it makes no sense", noun, v, noun, v)
			}
		}
	}
	for noun := range rowKindVerbExceptions {
		if have, ok := verbs[noun]; !ok || !slices.Contains(have, "add") || !slices.Contains(have, "search") {
			t.Errorf("rowKindVerbExceptions names %s, which is no row kind any more", noun)
		}
	}
	// the owner's case and the kinds it was found with stay covered
	for _, name := range []string{"cookbookrecipe.archive", "cookbookrecipe.unarchive", "architecturenote.archive", "behaviour.archive", "incident.archive", "suggestion.archive", "tastepref.archive", "workflow.archive", "workspace.archive", "techdoc.archive", "techdoc.edit"} {
		noun, verb, _ := strings.Cut(name, ".")
		if !slices.Contains(verbs[noun], verb) {
			t.Errorf("%s is not in the registry", name)
		}
	}
	if kinds < 20 {
		t.Fatalf("found %d row kinds; the add+search rule no longer finds them", kinds)
	}
}
