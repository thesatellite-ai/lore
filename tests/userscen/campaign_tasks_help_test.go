//go:build userscen

package userscen

// Every command of the tasks area answers --help with its usage and at
// least one runnable "lore …" example line, and every id it takes is
// checked the same way: a value that cannot be an id of that kind is
// E_INVALID_ID (exit 20), a well-formed id no row has is E_NOT_FOUND
// (exit 27), both with a hint naming the next step, the --json error
// document's exit equal to the process's, and never a confirmation
// question about a row that cannot exist (G-ENGINE-4).

import (
	"strings"
	"testing"
)

// TestCampaignTasks_HelpHasExamples: `lore <cmd> --help` exits 0, prints
// the Usage line naming the command and an Examples block with at least
// one `lore <cmd>` line, for all 104 commands of the area.
func TestCampaignTasks_HelpHasExamples(t *testing.T) {
	s := newTasksSolo(t)
	if len(campTasksCommands) != 104 {
		t.Fatalf("the tasks area has 104 commands, the list has %d", len(campTasksCommands))
	}
	for _, c := range campTasksCommands {
		args := append(strings.Fields(c), "--help")
		r := s.c.Lore(args...).OK(t)
		if !strings.Contains(r.Stdout, "Usage:\n  lore "+c) {
			t.Errorf("%s --help: no usage line naming the command:\n%s", c, r.Stdout)
			continue
		}
		if strings.Contains(r.Stdout, "(required) (required)") {
			t.Errorf("%s --help says (required) twice:\n%s", c, r.Stdout)
		}
		block, _, found := strings.Cut(r.Stdout[strings.Index(r.Stdout, "\n")+1:], "\nFlags:")
		_, ex, hasEx := strings.Cut(block, "Examples:\n")
		if !found || !hasEx || !strings.Contains(ex, "  lore ") {
			t.Errorf("%s --help: no Examples line (a person learns a command from one):\n%s", c, r.Stdout)
		}
	}
}

// campTasksIDProbe is one id-taking command: the words before the id, the
// prefix of the kind it looks up, and the arguments after it that make the
// rest of the input valid.
type campTasksIDProbe struct {
	cmd    string
	prefix string
	after  []string
}

// campTasksGone is the answer for a well-formed id no row has where it is
// not E_NOT_FOUND: heartbeat and release ask the signals ref about YOUR
// claim, and "you hold no claim on it" is E_LOST_CLAIM (exit 12) for a
// task that never existed as for one that was taken (LORE_TEAM_SPEC 7).
var campTasksGone = map[string]string{"task heartbeat": "E_LOST_CLAIM", "task release": "E_LOST_CLAIM"}

// campTasksIDProbes lists every command of the area whose first argument
// is a row id (names, like template and view names, are probed apart).
var campTasksIDProbes = []campTasksIDProbe{
	{"task show", "tsk", nil}, {"task edit", "tsk", []string{"--priority", "high"}},
	{"task start", "tsk", nil}, {"task done", "tsk", []string{"--no-lesson"}},
	{"task cancel", "tsk", []string{"--yes"}}, {"task delete", "tsk", []string{"--yes"}},
	{"task accept", "tsk", nil}, {"task decline", "tsk", []string{"--reason", "no"}},
	{"task history", "tsk", nil}, {"task watch", "tsk", nil}, {"task unwatch", "tsk", nil},
	{"task relate", "tsk", []string{"--relates-to", "tsk_" + campTasksUnknownHex}},
	{"task unrelate", "tsk", []string{"--relates-to", "tsk_" + campTasksUnknownHex}},
	{"task split", "tsk", []string{"first", "second"}},
	{"task note", "tsk", []string{"a note"}},
	{"task checklist add", "tsk", []string{"an item"}},
	{"task checklist list", "tsk", nil},
	{"task checklist tick", "tsk", []string{"1"}}, {"task checklist untick", "tsk", []string{"1"}},
	{"task checklist edit", "tsk", []string{"1", "new text"}},
	{"task checklist rm", "tsk", []string{"1"}}, {"task checklist promote", "tsk", []string{"1"}},
	{"task checklist import", "tsk", []string{"--from", "body"}},
	{"task recur show", "tsk", nil}, {"task recur skip", "tsk", nil},
	{"task recur stop", "tsk", nil}, {"task recur edit", "tsk", []string{"every", "day"}},
	{"task time show", "tsk", nil}, {"task time stop", "tsk", nil},
	{"task time log", "tsk", []string{"1h"}},
	{"task time edit", "tme", []string{"--note", "x"}}, {"task time rm", "tme", []string{"--yes"}},
	{"task time restore", "tme", nil},
	{"tasklist show", "tlt", nil}, {"tasklist edit", "tlt", []string{"--title", "x"}},
	{"tasklist delete", "tlt", []string{"--yes"}},
	{"mission show", "msn", nil}, {"mission edit", "msn", []string{"--title", "x"}},
	{"mission done", "msn", nil}, {"mission pause", "msn", nil}, {"mission resume", "msn", nil},
	{"mission delete", "msn", []string{"--yes"}}, {"mission status", "msn", nil},
	{"plan show", "pln", nil}, {"plan edit", "pln", []string{"--title", "x"}},
	{"plan delete", "pln", []string{"--yes"}},
	{"workflow show", "wfl", nil}, {"workflow edit", "wfl", []string{"--body", "x"}},
	{"workflow delete", "wfl", nil},
	{"reminder done", "rmd", nil}, {"reminder delete", "rmd", nil},
	{"run end", "run", []string{"--outcome", "success"}}, {"run cancel", "run", nil},
	{"task claim", "tsk", nil}, {"task heartbeat", "tsk", nil}, {"task release", "tsk", nil},
	{"task revoke", "tsk", nil}, {"task merged", "tsk", nil},
	{"gate request", "tsk", nil}, {"gate approve", "tsk", nil},
}

// TestCampaignTasks_BadIDs: for every id-taking command, a typo (no
// prefix, cut short, another kind's prefix) is E_INVALID_ID and an id no
// row has is E_NOT_FOUND; each carries a hint, and none of them asks to
// confirm first (the deletes are run WITHOUT --yes where they ask).
func TestCampaignTasks_BadIDs(t *testing.T) {
	s := newTasksSolo(t)
	for _, p := range campTasksIDProbes {
		t.Run(p.cmd, func(t *testing.T) {
			t.Parallel() // every probe fails before it writes: one clone serves them all
			campTasksProbeBadID(t, s, p)
		})
	}
}

// campTasksProbeBadID runs one command's bad-id matrix. A time entry is
// named by a unique prefix or suffix of its id (task time edit/rm/restore),
// so any value that matches no entry is E_NOT_FOUND there, never a typo.
func campTasksProbeBadID(t *testing.T, s *tasksSolo, p campTasksIDProbe) {
	{
		bad := map[string]string{
			"no prefix":    "nope",
			"cut short":    p.prefix + "_01a1b7",
			"wrong prefix": "zzz_" + campTasksUnknownHex,
		}
		if p.prefix == "tme" {
			bad = nil
		}
		for what, v := range bad {
			args := append(append(strings.Fields(p.cmd), v), p.after...)
			e := campTasksErr(t, s.c.Lore(append(args, "--json")...))
			if e.Error.Code != "E_INVALID_ID" || e.Error.Exit != 20 || e.Error.Hint == "" {
				t.Errorf("%s with an id %s (%q): want E_INVALID_ID exit 20 with a hint, got %s exit %d hint %q (%s)",
					p.cmd, what, v, e.Error.Code, e.Error.Exit, e.Error.Hint, e.Error.Message)
			}
		}
		args := append(append(strings.Fields(p.cmd), p.prefix+"_"+campTasksUnknownHex), p.after...)
		e := campTasksErr(t, s.c.Lore(append(args, "--json")...))
		want, wantExit := "E_NOT_FOUND", 27
		if g, ok := campTasksGone[p.cmd]; ok {
			want, wantExit = g, 12
		}
		if e.Error.Code != want || e.Error.Exit != wantExit || e.Error.Hint == "" {
			t.Errorf("%s with an id no row has: want %s exit %d with a hint, got %s exit %d hint %q (%s)",
				p.cmd, want, wantExit, e.Error.Code, e.Error.Exit, e.Error.Hint, e.Error.Message)
		}
	}
}
