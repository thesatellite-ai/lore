//go:build journeys && !windows

package journeys

// gitexec_budget_test.go — every everyday command starts a bounded number
// of git processes (devco:D-418). One `task list` once started 121 git
// processes; at ~20 ms per process on macOS that alone was most of a
// second. The gitfast Session answers repository reads in-process now;
// these budgets keep the count from creeping back. A command over its
// budget fails with the git argv it ran, so the new call is easy to find.

import (
	"strings"
	"testing"
)

// gitBudget is the most git processes one command may start (counted by
// the World's PATH shim; hooks git runs inside are not counted). The
// owner's targets are a read at most 5 and a write at most 10; each budget
// is the measured count plus one, so any new git call on these paths fails.
var gitBudget = []struct {
	name string
	args []string
	max  int
}{
	{"task list", []string{"task", "list"}, 2},
	{"task show", nil, 2}, // args filled in with the task id
	{"search", []string{"search", "--query", "budget"}, 2},
	{"render", []string{"render"}, 4},
	{"task add", []string{"task", "add", "Second budget task", "--commitment", "accepted"}, 8},
	{"memory add", []string{"memory", "add", "--body", "budget memory"}, 2},
	{"doctor", []string{"doctor"}, 8},
}

func TestJourneyGitExecBudget(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dev := w.Persona("Bea Budget", "bea.budget@example.test")
	m := dev.Laptop()
	app := m.Clone(w.Remote("github.com/bea/abacus"), "abacus")
	var id string
	w.Step("day1/init", func() {
		app.Lore("init", "--non-interactive", "--name=abacus").OK(t)
		app.Lore("task", "add", "Budget task one", "--commitment", "accepted").OK(t)
		out := app.Lore("task", "list", "--json").OK(t).Stdout
		i := strings.Index(out, "tsk_")
		if i < 0 {
			t.Fatalf("no task id in %q", out)
		}
		id = out[i : i+4+32]
	})
	w.Step("day1/git-exec-budgets", func() {
		for _, c := range gitBudget {
			args := c.args
			if args == nil {
				args = []string{"task", "show", "--id", id}
			}
			before := len(w.GitCalls(m.Name))
			if r := app.Lore(args...); r.Code != 0 && c.name != "doctor" { // doctor exits 1 on a warning
				t.Fatalf("%s: exit %d: %s", c.name, r.Code, r.Out())
			}
			calls := w.GitCalls(m.Name)[before:]
			t.Logf("%s: %d git processes (budget %d)", c.name, len(calls), c.max)
			if len(calls) > c.max {
				t.Errorf("%s started %d git processes, budget %d (devco:D-418):\n  %s", c.name, len(calls), c.max, strings.Join(calls, "\n  "))
			}
		}
	})
}

// gitBudgetTeam is gitBudget for a repo whose org has a team store and a
// personal store (each command also reads both store clones).
var gitBudgetTeam = map[string]int{
	"task list": 3, "task show": 4, "search": 3, "render": 3, "task add": 5, "memory add": 7, "doctor": 30,
}

func TestJourneyGitExecBudgetTeam(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	repo := tt.repo[tlLee.Email]
	var id string
	w.Step("day1/team-store-attached", func() {
		tlStartTeam(t, lee, repo.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.LoreIn(repo, "store", "folder", "add", "team", "app").OK(t)
		lee.LoreIn(repo, "org", "link", tlOrg, "--project", "app", "--yes").OK(t)
		lee.LoreIn(repo, "task", "add", "Budget task one", "--commitment", "accepted").OK(t)
		out := lee.LoreIn(repo, "task", "list", "--json").OK(t).Stdout
		i := strings.Index(out, "tsk_")
		if i < 0 {
			t.Fatalf("no task id in %q", out)
		}
		id = out[i : i+4+32]
	})
	w.Step("day1/git-exec-budgets-with-stores", func() {
		for _, c := range gitBudget {
			args := c.args
			if args == nil {
				args = []string{"task", "show", "--id", id}
			}
			max := gitBudgetTeam[c.name]
			before := len(w.GitCalls(lee.Name))
			if r := lee.LoreIn(repo, args...); r.Code != 0 && c.name != "doctor" {
				t.Fatalf("%s: exit %d: %s", c.name, r.Code, r.Out())
			}
			calls := w.GitCalls(lee.Name)[before:]
			t.Logf("%s: %d git processes (budget %d)", c.name, len(calls), max)
			if len(calls) > max {
				t.Errorf("%s started %d git processes with a team store, budget %d (devco:D-418):\n  %s", c.name, len(calls), max, strings.Join(calls, "\n  "))
			}
		}
	})
}
