//go:build userscen

package userscen

// Repeating tasks through the real binary: a weekly task's next instance
// after done, done twice (no second instance), a done weeks late (the
// missed dates are skipped, never piled up), every! (from completion),
// recur skip (this instance closed as skipped, the next one made), a
// series with "for N" that ends, recur stop, and bad rules refused.

import (
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tasksSeries is the open and closed instances of one repeating title.
func tasksSeries(t *testing.T, c *th.Clone, title string) []tasksTask {
	t.Helper()
	env := th.DecodeJSON[tasksListEnvelope[tasksTask]](t, c.Lore("task", "list", "--all", "--json"))
	var out []tasksTask
	for _, d := range env.Data {
		if d.Title == title {
			out = append(out, d)
		}
	}
	return out
}

// tasksOpenDue returns the due dates (YYYY-MM-DD) of the open instances.
func tasksOpenDue(series []tasksTask) []string {
	var out []string
	for _, d := range series {
		if d.Status != "done" && d.Status != "cancelled" {
			out = append(out, d.DueAt[:min(len(d.DueAt), 10)])
		}
	}
	return out
}

// Weekly: done makes the next week's instance once, whatever the number
// of dones; a done three weeks late skips the missed weeks.
func TestTasksRecur_WeeklyDoneTwiceAndLate(t *testing.T) {
	s := newTasksSolo(t)
	const title = "weekly report"
	first := tasksAdd(t, s.c, title, "--recur", "every week", "--due", "2026-11-02")
	s.c.MustLore("task", "recur", "show", first).Says(t, "every week", "2026-11-09")
	s.c.MustLore("task", "done", first, "--no-lesson").Says(t, "next instance", "due 2026-11-09")
	s.c.MustLore("task", "done", first, "--no-lesson").Says(t, "was already done by", "nothing changed")
	if due := tasksOpenDue(tasksSeries(t, s.c, title)); len(due) != 1 || due[0] != "2026-11-09" {
		t.Fatalf("one open instance due 2026-11-09 after two dones: %v", due)
	}
	var second string
	for _, d := range tasksSeries(t, s.c, title) {
		if d.Status == "todo" {
			second = d.ID
		}
	}
	s.a.SetNow(time.Date(2026, 11, 30, 9, 0, 0, 0, time.UTC))
	s.c.MustLore("task", "done", second, "--no-lesson").Says(t, "skipped 2 past dates", "due 2026-11-30")
	if due := tasksOpenDue(tasksSeries(t, s.c, title)); len(due) != 1 || due[0] != "2026-11-30" {
		t.Fatalf("a late done leaves one open instance, not a pile of missed weeks: %v", due)
	}
}

// every! counts from the completion, not from the due date.
func TestTasksRecur_FromCompletion(t *testing.T) {
	s := newTasksSolo(t)
	const title = "water plants"
	id := tasksAdd(t, s.c, title, "--recur", "every! 3 days", "--due", "2026-11-02")
	s.a.SetNow(time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC))
	s.c.MustLore("task", "done", id, "--no-lesson").Says(t, "due 2026-11-10")
	if due := tasksOpenDue(tasksSeries(t, s.c, title)); len(due) != 1 || due[0] != "2026-11-10" {
		t.Fatalf("every! 3 days done on 11-07: next due 11-10: %v", due)
	}
}

// recur skip closes this instance as skipped (cancelled, close_reason
// skipped) and makes the next; recur stop ends the series: a done after
// it makes nothing.
func TestTasksRecur_SkipAndStop(t *testing.T) {
	s := newTasksSolo(t)
	const title = "pay rent"
	id := tasksAdd(t, s.c, title, "--recur", "every month on last", "--due", "2026-11-30")
	s.c.MustLore("task", "recur", "skip", id)
	if got := tasksShow(t, s.c, id); got.Status != "cancelled" || got.CloseReason != "skipped" {
		t.Fatalf("a skipped instance is cancelled with close_reason skipped: %+v", got)
	}
	due := tasksOpenDue(tasksSeries(t, s.c, title))
	if len(due) != 1 || due[0] != "2026-12-31" {
		t.Fatalf("the skip made the next month-end instance: %v", due)
	}
	var next string
	for _, d := range tasksSeries(t, s.c, title) {
		if d.Status == "todo" {
			next = d.ID
		}
	}
	s.c.Lore("task", "recur", "stop", next).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "--yes")
	s.c.MustLore("task", "recur", "stop", next, "--yes")
	s.c.MustLore("task", "done", next, "--no-lesson")
	if due := tasksOpenDue(tasksSeries(t, s.c, title)); len(due) != 0 {
		t.Fatalf("a stopped series makes no more instances: %v", due)
	}
}

// "for N" ends the series after N instances.
func TestTasksRecur_ForNEnds(t *testing.T) {
	s := newTasksSolo(t)
	const title = "two day trial"
	id := tasksAdd(t, s.c, title, "--recur", "every day for 2", "--due", "2026-11-02")
	s.c.MustLore("task", "done", id, "--no-lesson")
	due := tasksOpenDue(tasksSeries(t, s.c, title))
	if len(due) != 1 || due[0] != "2026-11-03" {
		t.Fatalf("instance 2 of 2 is due 11-03: %v", due)
	}
	var second string
	for _, d := range tasksSeries(t, s.c, title) {
		if d.Status == "todo" {
			second = d.ID
		}
	}
	s.c.MustLore("task", "done", second, "--no-lesson")
	if due := tasksOpenDue(tasksSeries(t, s.c, title)); len(due) != 0 {
		t.Fatalf("after the 2nd of 2 there is no 3rd: %v", due)
	}
}

// Rules lore cannot read are refused with nothing added.
func TestTasksRecur_BadRules(t *testing.T) {
	s := newTasksSolo(t)
	for _, rule := range []string{"every blue moon", "every 0 days", "sometimes", "every 2 fortnights"} {
		r := s.c.Lore("task", "add", "bad recur "+rule, "--recur", rule)
		if r.Code == 0 {
			t.Fatalf("--recur %q is refused: %s", rule, r)
		}
		if len(tasksSeries(t, s.c, "bad recur "+rule)) != 0 {
			t.Fatalf("a refused --recur %q adds nothing", rule)
		}
	}
	plain := tasksAdd(t, s.c, "not repeating")
	s.c.Lore("task", "recur", "skip", plain).Refused(t)
}
