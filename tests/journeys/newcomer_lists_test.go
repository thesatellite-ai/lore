//go:build journeys && !windows

package journeys

// newcomer_lists_test.go — a newcomer reads the lists (bug hunt B3-7,
// B3-8, B3-10, B3-11, B3-18, B3-24). Nina has one repo and a personal store. She
// adds a note to each, a rule for one folder, her first task, then deletes
// it, and reads every list on the way.
//
// What she checks: a list prints every row in one layout whatever store
// it comes from (the personal row only adds its store label); the first
// task add says it created the default tasklist; tasklist list shows its
// title, status and task count; the hidden-rows footer calls a folder rule
// "scoped to a folder" and names it; LORE.md marks the folder rule's
// scope; trash list names who deleted the task.

import (
	"regexp"
	"strings"
	"testing"
)

// nlListRow is a memory or rule list row: "<id> [<kind>] <body>[ [label]]".
var nlListRow = regexp.MustCompile(`^(mem|rul)_[0-9a-f]{32} \[[a-z/]+\] \S`)

func TestJourneyNewcomerLists(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nina := w.Persona("Nina Newcomer", "nina@example.test")
	m := nina.Laptop()
	app := m.Clone(w.Remote("github.com/nina/shop"), "shop")

	w.Step("day1/notes-in-two-stores-one-layout", func() {
		app.Lore("init", "--non-interactive", "--name=shop").OK(t)
		app.Lore("store", "init", "me").OK(t)
		app.Lore("memory", "add", "--body", "deploys run on merge").OK(t)
		app.Lore("memory", "add", "--body", "I like short commits", "--to", "me").OK(t)
		app.Lore("rule", "add", "--body", "tests first").OK(t)
		app.Lore("rule", "add", "--body", "no force push", "--to", "me").OK(t)
		for _, noun := range []string{"memory", "rule"} {
			out := app.Lore(noun, "list").OK(t).Stdout
			rows := 0
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if strings.HasPrefix(line, "mem_") || strings.HasPrefix(line, "rul_") {
					rows++
					if !nlListRow.MatchString(line) {
						t.Errorf("%s list row in another layout: %q\n%s", noun, line, out)
					}
				}
			}
			if rows != 2 || !strings.Contains(out, " [me]") {
				t.Errorf("%s list: want 2 rows, the personal one labelled [me]:\n%s", noun, out)
			}
		}
	})

	w.Step("day1/folder-rule-named-in-list-and-lore-md", func() {
		app.WriteFile("src/booking/keep.txt", "x\n")
		app.Lore("rule", "add", "--body", "Booking dates are stored in UTC", "--scope", "dir:src/booking").OK(t)
		app.Lore("rule", "list").OK(t).Says(t, "1 hidden (1 scoped to a folder (dir:src/booking))").Never(t, "scoped to a repo")
		app.Lore("render").OK(t)
		md, _ := app.ReadFile(".lore/LORE.md")
		if !strings.Contains(md, `scope="dir:src/booking"`) && !strings.Contains(md, "(only in src/booking/)") {
			t.Errorf("LORE.md does not mark the folder rule's scope:\n%s", md)
		}
	})

	w.Step("day1/first-task-tells-of-its-tasklist", func() {
		add := app.Lore("task", "add", "Check the home page links", "--commitment", "accepted").OK(t)
		add.Says(t, `created tasklist tlt_`, `"default"`)
		app.Lore("task", "add", "Second task", "--commitment", "accepted").OK(t).Never(t, "created tasklist")
		app.Lore("tasklist", "list").OK(t).Says(t, "default [active]  (2 tasks)")
		id := regexp.MustCompile(`tsk_[0-9a-f]{32}`).FindString(add.Stdout)
		app.Lore("task", "delete", id, "--yes").OK(t)
		app.Lore("trash", "list").OK(t).Says(t, "by Nina Newcomer").Never(t, "by act_")
	})

	// B3-24: show names its writers, help carries no spec ids, an import
	// dry run says the tasks get new ids
	w.Step("day2/show-help-and-import-read-plainly", func() {
		mem := regexp.MustCompile(`mem_[0-9a-f]{32}`).FindString(app.Lore("memory", "add", "--body", "the shop opens at nine").OK(t).Stdout)
		app.Lore("show", mem).OK(t).Says(t, "by Nina Newcomer").Never(t, "by act_")
		app.Lore("move", "--help").OK(t).Never(t, "MQ11", "BATCH 7", "TQ3")
		csv := app.Lore("task", "export", "--format", "csv").OK(t).Stdout
		app.WriteFile("tasks.csv", csv)
		app.Lore("task", "import", "tasks.csv", "--dry-run").OK(t).Says(t, "as new tasks with new ids")
		md, _ := app.ReadFile(".lore/LORE.md")
		if strings.Contains(md, "AICODER") || strings.Contains(md, " 1 memories") {
			t.Errorf("LORE.md has the bare canary marker or a wrong plural:\n%s", md)
		}
	})
}
