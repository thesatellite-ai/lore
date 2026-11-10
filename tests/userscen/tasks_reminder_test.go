//go:build userscen

package userscen

// Reminders with a pinned clock (LORE_NOW): a reminder is listed with its
// date, marked overdue (!) once the date has passed, done once (it leaves
// the pending list and shows under --done; a second done is harmless), a
// recurring one moves forward by its period, a monthly one on the 31st
// lands at the end of the next month (never skipping a month), and bad
// dates or recurrences are refused with the valid set.

import (
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tasksReminder is one `lore reminder list --json` row.
type tasksReminder struct {
	ID         string `json:"id"`
	Message    string `json:"message"`
	DueAt      string `json:"due_at"`
	Recurrence string `json:"recurrence"`
}

// tasksReminders lists the pending (or with --done, the completed)
// reminders.
func tasksReminders(t *testing.T, c *th.Clone, extra ...string) []tasksReminder {
	t.Helper()
	env := th.DecodeJSON[tasksListEnvelope[tasksReminder]](t, c.Lore(append([]string{"reminder", "list", "--json"}, extra...)...))
	if env.Kind != "reminder.list" || env.Count != len(env.Data) {
		t.Fatalf("reminder list --json envelope: kind %q count %d rows %d", env.Kind, env.Count, len(env.Data))
	}
	return env.Data
}

// tasksReminderAdd adds a reminder and returns its id.
func tasksReminderAdd(t *testing.T, c *th.Clone, msg string, extra ...string) string {
	t.Helper()
	return th.DecodeJSON[struct {
		ID string `json:"id"`
	}](t, c.Lore(append([]string{"reminder", "add", msg, "--json"}, extra...)...)).ID
}

// One-shot: listed, overdue after its date, done once and twice.
func TestTasksReminder_FiresAndCompletes(t *testing.T) {
	s := newTasksSolo(t)
	id := tasksReminderAdd(t, s.c, "renew the TLS cert", "--due", "2026-11-05")
	if got := tasksReminders(t, s.c); len(got) != 1 || got[0].ID != id || !strings.HasPrefix(got[0].DueAt, "2026-11-05") {
		t.Fatalf("the new reminder is pending with its date: %+v", got)
	}
	s.c.MustLore("reminder", "list").Says(t, "2026-11-05  renew the TLS cert").Never(t, "! 2026-11-05")
	s.a.SetNow(time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))
	s.c.MustLore("reminder", "list").Says(t, "! 2026-11-05  renew the TLS cert")
	s.c.MustLore("reminder", "done", id).Says(t, "completed")
	s.c.MustLore("reminder", "done", id).Says(t, "completed")
	if got := tasksReminders(t, s.c); len(got) != 0 {
		t.Fatalf("a done reminder leaves the pending list: %+v", got)
	}
	if got := tasksReminders(t, s.c, "--done"); len(got) != 1 || got[0].ID != id {
		t.Fatalf("--done shows it: %+v", got)
	}
}

// Recurring reminders done on time move by their period from the due
// date; a monthly one on a month's last day lands on the next month's last
// day, never in the month after (Jan 31 + 1m is Feb 28, not Mar 3). The
// month-end dues are in the future of the pinned clock: one done late
// skips to the first date after now (TestTasksReminder_DoneLateSkipsPast).
func TestTasksReminder_RecurringMovesForward(t *testing.T) {
	s := newTasksSolo(t)
	cases := []struct{ due, rec, want string }{
		{"2026-11-05", "7d", "2026-11-12"},
		{"2026-11-05", "30d", "2026-12-05"},
		{"2026-11-05", "1m", "2026-12-05"},
		{"2027-01-31", "1m", "2027-02-28"},
		{"2027-03-31", "1m", "2027-04-30"},
		{"2026-08-31", "3m", "2026-11-30"},
		{"2026-08-31", "6m", "2027-02-28"},
		{"2028-02-29", "1y", "2029-02-28"},
		{"2026-12-31", "1m", "2027-01-31"},
	}
	for _, tc := range cases {
		id := tasksReminderAdd(t, s.c, "every "+tc.rec+" from "+tc.due, "--due", tc.due, "--recurrence", tc.rec)
		s.c.MustLore("reminder", "done", id).Says(t, "rescheduled to "+tc.want)
		var due string
		for _, r := range tasksReminders(t, s.c) {
			if r.ID == id {
				due = r.DueAt
			}
		}
		if !strings.HasPrefix(due, tc.want) {
			t.Fatalf("%s from %s: the reminder stays pending, due %s; got %q", tc.rec, tc.due, tc.want, due)
		}
	}
}

// TestTasksReminder_DoneLateSkipsPast (INTEG-TRASH-5): a weekly reminder
// done a month after its date moves to the first date after now, says how
// many it skipped, and is not overdue again at once (it moved one period,
// 2026-10-08, and needed four more dones).
func TestTasksReminder_DoneLateSkipsPast(t *testing.T) {
	s := newTasksSolo(t) // now: 2026-11-02 09:00 UTC
	id := tasksReminderAdd(t, s.c, "water the plants", "--due", "2026-10-01", "--recurrence", "7d")
	s.c.MustLore("reminder", "list").Says(t, "! 2026-10-01  water the plants")
	s.c.MustLore("reminder", "done", id).Says(t, "rescheduled to 2026-11-05", "skipped 4 past dates")
	s.c.MustLore("reminder", "list").Says(t, "2026-11-05  water the plants").Never(t, "! 2026-11-05")
}

// Bad input: a date lore cannot read, a recurrence outside the set, no
// message, an id of another kind.
func TestTasksReminder_BadInput(t *testing.T) {
	s := newTasksSolo(t)
	s.c.Lore("reminder", "add", "x", "--due", "2026-13-40").Fails(t, tasksExitBadInput, "E_INVALID_INPUT", "YYYY-MM-DD")
	s.c.Lore("reminder", "add", "x", "--due", "2026-11-05", "--recurrence", "2w").Fails(t, tasksExitBadInput, "E_INVALID_INPUT", "7d | 30d | 1m | 3m | 6m | 1y")
	s.c.Lore("reminder", "add", "x").Refused(t, "due")
	s.c.Lore("reminder", "done", "tsk_01a1b4aef035716fb58ae56ab97530b2").Refused(t, "E_INVALID_ID")
	if got := tasksReminders(t, s.c); len(got) != 0 {
		t.Fatalf("refused adds write nothing: %+v", got)
	}
}
