//go:build journeys && !windows

package journeys

// i18n_test.go — a person west of UTC and a Turkish-speaking team (round
// 3 track I, G-5): reminder and task lists show a clock-time due on the
// day it falls where the person is, a date-only due is not flagged before
// that day has begun there, an overdue reminder shows in `lore inbox`, and
// search meets i, ı, I and İ as one letter, showing the text as written.

import (
	"strings"
	"testing"
	"time"
)

func TestJourneyLocalDaysAndTurkishSearch(t *testing.T) {
	t.Parallel()
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tz database")
	}
	w := NewWorld(t)
	ana := w.Persona("Ana Vale", "ana@westcoast.example")
	lap := ana.Laptop()
	lap.SetEnv("TZ", "America/Los_Angeles")
	repo := lap.Clone(w.Remote("github.com/westcoast-dev/app"), "app")
	tomorrow := w.Now().In(la).AddDate(0, 0, 1).Format("2006-01-02")

	w.Step("day1/evening-due-is-listed-on-its-local-day", func() {
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "reminder", "add", "--message", "call the vendor", "--due", "tomorrow 20:00").OK(t).Says(t, "due "+tomorrow)
		lap.LoreIn(repo, "reminder", "add", "--message", "file the report", "--due", tomorrow).OK(t)
		list := lap.LoreIn(repo, "reminder", "list").OK(t)
		for _, line := range strings.Split(list.Stdout, "\n") {
			if strings.Contains(line, "call the vendor") || strings.Contains(line, "file the report") {
				if !strings.HasPrefix(line, "  "+tomorrow+"  ") {
					t.Errorf("reminder line %q: want due %s, not flagged", line, tomorrow)
				}
			}
		}
		lap.LoreIn(repo, "task", "add", "--title", "ship the build", "--due", "tomorrow 20:00", "--commitment", "accepted").OK(t)
		lap.LoreIn(repo, "task", "list").OK(t).Says(t, "due:"+tomorrow)
	})

	w.Step("day1/overdue-reminder-shows-in-the-inbox", func() {
		lap.LoreIn(repo, "reminder", "add", "--message", "renew the TLS cert", "--due", "2025-12-15").OK(t)
		lap.LoreIn(repo, "inbox").OK(t).Says(t, "renew the TLS cert (overdue, 2025-12-15").Never(t, "call the vendor", "(inbox empty)")
	})

	w.Step("day1/turkish-letters-meet-in-search", func() {
		lap.LoreIn(repo, "memory", "add", "--body", "İSTANBUL ıspanak DİYARBAKIR").OK(t)
		for _, q := range []string{"ISPANAK", "ıspanak", "diyarbakır", "istanbul"} {
			lap.LoreIn(repo, "search", q).OK(t).Says(t, "ıspanak").Never(t, "(no matches)")
		}
	})
}
