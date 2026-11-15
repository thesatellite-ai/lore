//go:build journeys && !windows

package journeys

// recur_clock_test.go — round 7 track T (devco:T-8, T-9, T-10, T-16). Ana
// runs a 09:00 weekly standup from New York: the reminder and the
// recurring task keep 09:00 across the March DST change, the task's time
// shows in task show and --json, a monthly "on last" series started on
// Jan 30 never lists two January dates, and the trash names who deleted
// rows and carries no spec id in its errors.

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestJourneyRecurringKeepsTheClock(t *testing.T) {
	t.Parallel()
	if _, err := time.LoadLocation("America/New_York"); err != nil {
		t.Skip("no tz database")
	}
	w := NewWorld(t)
	ana := w.Persona("Ana Vale", "ana@eastcoast.example")
	lap := ana.Laptop()
	lap.SetEnv("TZ", "America/New_York")
	repo := lap.Clone(w.Remote("github.com/eastcoast-dev/app"), "app")
	id := regexp.MustCompile(`(rmd|tsk|mem)_[0-9a-f]{32}`)

	w.Step("day1/weekly-reminder-keeps-09:00-across-dst", func() {
		lap.LoreIn(repo, "init").OK(t)
		out := lap.LoreIn(repo, "reminder", "add", "--due", "2027-03-13 + 09:00", "--recurrence", "7d", "--message", "standup").OK(t)
		lap.LoreIn(repo, "reminder", "done", "--id", id.FindString(out.Stdout)).OK(t)
		lap.LoreIn(repo, "reminder", "list", "--json").OK(t).Says(t, "2027-03-20T13:00:00Z").Never(t, "2027-03-20T14:00:00Z")
	})

	w.Step("day1/task-due-time-is-kept", func() {
		out := lap.LoreIn(repo, "task", "add", "--title", "standup", "--due", "2027-03-12 + 09:00", "--recur", "every week", "--commitment", "accepted").OK(t)
		tsk := id.FindString(out.Stdout)
		lap.LoreIn(repo, "task", "show", "--id", tsk).OK(t).Says(t, "2027-03-12 09:00 EST")
		lap.LoreIn(repo, "task", "show", "--id", tsk, "--json").OK(t).Says(t, "2027-03-12T14:00:00Z")
		lap.LoreIn(repo, "task", "done", "--id", tsk, "--no-lesson").OK(t).Says(t, "2027-03-19 09:00 EDT")
	})

	w.Step("day1/month-on-last-never-twice-in-a-month", func() {
		lap.LoreIn(repo, "task", "add", "--title", "payroll check", "--due", "2026-01-30", "--recur", "every month on last", "--commitment", "accepted").OK(t).
			Says(t, "next: 2026-01-30, 2026-02-28, 2026-03-31").Never(t, "2026-01-31")
	})

	w.Step("day1/trash-names-the-deleter", func() {
		var mems []string
		for _, b := range []string{"one", "two"} {
			mems = append(mems, id.FindString(lap.LoreIn(repo, "memory", "add", "--body", "note "+b).OK(t).Stdout))
		}
		for _, m := range mems {
			lap.LoreIn(repo, "memory", "delete", "--id", m, "--yes").OK(t)
		}
		stats := lap.LoreIn(repo, "trash", "stats").OK(t)
		if by := stats.Stdout[strings.Index(stats.Stdout, "by deleter"):]; strings.Contains(strings.SplitN(by, "tomb", 2)[0], "act_") {
			t.Errorf("stats names an act_ id:\n%s", stats.Stdout)
		}
		lap.LoreIn(repo, "trash", "purge", "--id", mems[0], "--confirm").OK(t)
		lap.LoreIn(repo, "trash", "restore", "--id", mems[0]).Refused(t, "E_PURGED", "purged rows never come back").Never(t, "(DQ4)")
	})
}
