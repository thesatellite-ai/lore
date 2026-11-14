//go:build userscen

package userscen

// Bad flag values across the tasks area: each is refused with its exact
// code, a non-zero exit equal to the --json document's, a hint naming the
// next step (the allowed values, a date or rule to copy, the file to
// check), and writes nothing.

import (
	"strings"
	"testing"
)

// TestCampaignTasks_BadValues is the refusal matrix of odd flag values.
func TestCampaignTasks_BadValues(t *testing.T) {
	s := newTasksSolo(t)
	id := tasksAdd(t, s.c, "a task")
	prop := tasksAdd(t, s.c, "a proposal", "--commitment", "proposed")
	cases := []struct {
		args []string
		code string
	}{
		{[]string{"task", "add", "x", "--priority", "URGENT"}, "E_INVALID_INPUT"},
		{[]string{"task", "add", "x", "--commitment", "maybe"}, "E_INVALID_INPUT"},
		{[]string{"task", "add", "x", "--due", "notadate"}, "E_DATE"},
		{[]string{"task", "add", "x", "--defer-until", "the day after never"}, "E_DATE"},
		{[]string{"task", "add", "x", "--recur", "every blursday"}, "E_RECUR"},
		{[]string{"task", "add", "x", "--estimate", "-1"}, "E_INVALID_INPUT"},
		{[]string{"task", "add", ""}, "E_INVALID_INPUT"},
		{[]string{"task", "edit", id, "--after", id}, "E_CYCLE"},
		{[]string{"task", "export", "--format", "xml"}, "E_INVALID_INPUT"},
		{[]string{"task", "board", "--group-by", "bogus"}, "E_INVALID_INPUT"},
		{[]string{"task", "list", "--view", "no_such"}, "E_NOT_FOUND"},
		{[]string{"task", "import", "--from", "csv", "/nonexistent/tasks.csv"}, "E_INVALID_INPUT"},
		{[]string{"task", "import", "--from", "yaml", "x"}, "E_INVALID_INPUT"},
		{[]string{"task", "split", id, "--from", "/nonexistent/titles.txt"}, "E_INVALID_INPUT"},
		{[]string{"task", "time", "log", id, "0m"}, "E_DURATION_BAD"},
		{[]string{"task", "time", "log", id, "99999h"}, "E_TIME_TOO_LONG"},
		{[]string{"task", "checklist", "tick", id, "99"}, "E_NOT_FOUND"},
		{[]string{"task", "decline", prop, "--reason", "not now"}, "E_INVALID_INPUT"},
		{[]string{"task", "decline", id, "--reason", "wont_do"}, "E_REFUSED"}, // only a proposal can be declined
		{[]string{"mission", "add", "x", "--target", "notadate"}, "E_DATE"},
		{[]string{"reminder", "add", "x", "--due", "someday-ish"}, "E_DATE"}, // one date parser for every --due (batch 5)
		{[]string{"run", "start", "--task", "nope"}, "E_INVALID_INPUT"},
		{[]string{"run", "end", "run_" + campTasksUnknownHex, "--outcome", "bogus"}, "E_INVALID_INPUT"},
		{[]string{"inbox", "show", "--since", "bogus"}, "E_INVALID_INPUT"},
		{[]string{"plan", "edit", "pln_" + campTasksUnknownHex}, "E_INVALID_INPUT"},
		{[]string{"tasklist", "edit", "tlt_" + campTasksUnknownHex}, "E_INVALID_INPUT"},
	}
	s.c.MustLore("task", "list")
	before := campTasksTreeHash(t, s.c.Dir)
	for _, c := range cases {
		what := strings.Join(c.args, " ")
		e := campTasksErr(t, s.c.Lore(append(c.args, "--json")...))
		if e.Error.Code != c.code || e.Error.Hint == "" {
			t.Errorf("%s: want %s with a hint, got %s hint %q (%s)", what, c.code, e.Error.Code, e.Error.Hint, e.Error.Message)
		}
	}
	if campTasksTreeHash(t, s.c.Dir) != before {
		t.Error("a refused command changed the clone")
	}
}
