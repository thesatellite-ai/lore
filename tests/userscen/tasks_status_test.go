//go:build userscen

package userscen

// Task statuses and commitment levels through the real binary, the way a
// person drives them: start / done / cancel / edit --status in every
// order, reopening a closed task, cancel and decline reasons, the
// proposed / someday / deferred lists, accept and decline, and the exact
// refusals (code, exit, next step) for bad values and unknown ids.

import (
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tasksExitBadInput is the bad_input class exit (E_INVALID_INPUT,
// E_INVALID_ID, E_DATE).
const tasksExitBadInput = 20

// A closed task that is opened again (start, edit --status todo /
// in_progress / blocked) is open: it no longer carries the completed_at or
// close_reason of its last close, so "done today" reports and the export's
// completed_at column stop counting it. --reopen-parent already clears
// both; every reopen must.
func TestTasksStatus_ReopenClearsCloseFields(t *testing.T) {
	s := newTasksSolo(t)
	type reopen struct {
		name string
		args func(id string) []string
		want string
	}
	reopens := []reopen{
		{"start", func(id string) []string { return []string{"task", "start", id} }, "in_progress"},
		{"edit todo", func(id string) []string { return []string{"task", "edit", id, "--status", "todo"} }, "todo"},
		{"edit in_progress", func(id string) []string { return []string{"task", "edit", id, "--status", "in_progress"} }, "in_progress"},
		{"edit blocked", func(id string) []string { return []string{"task", "edit", id, "--status", "blocked"} }, "blocked"},
	}
	closes := map[string][]string{
		"done":   {"done", "--no-lesson"},
		"cancel": {"cancel"},
	}
	for closeName, closeArgs := range closes {
		for _, ro := range reopens {
			id := tasksAdd(t, s.c, "reopen "+closeName+" then "+ro.name)
			s.c.MustLore(append([]string{"task", closeArgs[0], id}, closeArgs[1:]...)...)
			closed := tasksShow(t, s.c, id)
			if closed.CloseReason == "" {
				t.Fatalf("%s: a closed task has a close_reason: %+v", closeName, closed)
			}
			if closeName == "done" && closed.CompletedAt == "" {
				t.Fatalf("done stamps completed_at: %+v", closed)
			}
			s.c.MustLore(ro.args(id)...)
			got := tasksShow(t, s.c, id)
			if got.Status != ro.want || got.CompletedAt != "" || got.CloseReason != "" {
				t.Fatalf("%s then %s: want status %s with no completed_at / close_reason, got %+v", closeName, ro.name, ro.want, got)
			}
		}
	}
}

// edit --status done is a close like `task done`: it stamps completed_at
// and close_reason completed; edit --status cancelled gives close_reason
// cancelled. A cancel after a done leaves no completed_at behind.
func TestTasksStatus_EditCloseStampsLikeDone(t *testing.T) {
	s := newTasksSolo(t)
	done := tasksAdd(t, s.c, "closed by edit")
	s.c.MustLore("task", "edit", done, "--status", "done")
	got := tasksShow(t, s.c, done)
	if got.Status != "done" || got.CompletedAt != tasksT0.Format(time.RFC3339) || got.CloseReason != "completed" {
		t.Fatalf("edit --status done: want completed_at %s, close_reason completed: %+v", tasksT0.Format(time.RFC3339), got)
	}
	cancelled := tasksAdd(t, s.c, "cancelled by edit")
	s.c.MustLore("task", "edit", cancelled, "--status", "cancelled")
	if got := tasksShow(t, s.c, cancelled); got.CloseReason != "cancelled" || got.CompletedAt != "" {
		t.Fatalf("edit --status cancelled: close_reason cancelled, no completed_at: %+v", got)
	}
	s.c.MustLore("task", "cancel", done)
	if got := tasksShow(t, s.c, done); got.Status != "cancelled" || got.CompletedAt != "" || got.CloseReason != "cancelled" {
		t.Fatalf("cancel after done: cancelled, no completed_at: %+v", got)
	}
}

// The whole walk a person does on one task, with the timestamps it
// leaves: started_at keeps the FIRST start, done stamps the clock of the
// done, and each step is visible in `task list` filters.
func TestTasksStatus_WalkKeepsFirstStartAndListsFollow(t *testing.T) {
	s := newTasksSolo(t)
	id := tasksAdd(t, s.c, "walk")
	if got := tasksShow(t, s.c, id); got.Status != "todo" || got.Priority != "medium" || got.Commitment != "accepted" || got.CreatedAt != tasksT0.Format(time.RFC3339) {
		t.Fatalf("a new task is todo / medium / accepted, created now: %+v", got)
	}
	s.c.MustLore("task", "start", id).Says(t, id+" started")
	s.a.Advance(time.Hour)
	s.c.MustLore("task", "edit", id, "--status", "blocked")
	if !tasksHas(tasksList(t, s.c, "--status", "blocked"), id) {
		t.Fatal("a blocked task is listed by --status blocked")
	}
	s.a.Advance(time.Hour)
	s.c.MustLore("task", "start", id)
	s.a.Advance(time.Hour)
	s.c.MustLore("task", "done", id, "--no-lesson").Says(t, id+" done")
	got := tasksShow(t, s.c, id)
	if got.StartedAt != tasksT0.Format(time.RFC3339) {
		t.Fatalf("started_at keeps the first start %s: %s", tasksT0.Format(time.RFC3339), got.StartedAt)
	}
	if got.CompletedAt != tasksT0.Add(3*time.Hour).Format(time.RFC3339) {
		t.Fatalf("completed_at is the done's clock: %s", got.CompletedAt)
	}
	if tasksHas(tasksList(t, s.c), id) {
		t.Fatal("the default list hides a done task")
	}
	if !tasksHas(tasksList(t, s.c, "--status", "done"), id) || !tasksHas(tasksList(t, s.c, "--all"), id) {
		t.Fatal("--status done and --all show a done task")
	}
}

// Cancel and decline take only the reasons their help names; a reason
// that belongs to another verb (completed is a done, skipped is a recur
// skip) is refused, so a cancelled task never reads as completed.
func TestTasksStatus_CancelReasons(t *testing.T) {
	s := newTasksSolo(t)
	for _, reason := range []string{"", "cancelled", "duplicate", "wont_do", "obsolete", "stale"} {
		id := tasksAdd(t, s.c, "cancel with "+reason)
		args := []string{"task", "cancel", id}
		if reason != "" {
			args = append(args, "--reason", reason)
		}
		s.c.MustLore(args...).Says(t, id+" cancelled")
		want := reason
		if want == "" {
			want = "cancelled"
		}
		if got := tasksShow(t, s.c, id); got.Status != "cancelled" || got.CloseReason != want {
			t.Fatalf("cancel --reason %q: status cancelled, close_reason %s: %+v", reason, want, got)
		}
	}
	for _, verb := range []string{"cancel", "decline"} {
		for _, bad := range []string{"completed", "skipped", "not now", "WONT_DO"} {
			id := tasksAdd(t, s.c, verb+" refused "+bad, "--commitment", "proposed")
			e := tasksJSONErr(t, s.c, "task", verb, id, "--reason", bad)
			if e.Error.Code != "E_INVALID_INPUT" || e.Error.Exit != tasksExitBadInput || !strings.Contains(e.Error.Message, "wont_do") {
				t.Fatalf("%s --reason %q: E_INVALID_INPUT naming the valid reasons: %+v", verb, bad, e)
			}
			if want := "(want: cancelled | duplicate | wont_do | obsolete | stale)"; !strings.Contains(e.Error.Message, want) {
				t.Fatalf("%s --reason lists only the reasons it takes %s: %s", verb, want, e.Error.Message)
			}
			if got := tasksShow(t, s.c, id); got.Status != "todo" || got.CloseReason != "" {
				t.Fatalf("a refused %s changes nothing: %+v", verb, got)
			}
		}
	}
	// decline defaults to wont_do and keeps --note as a comment.
	id := tasksAdd(t, s.c, "an idea", "--commitment", "proposed")
	s.c.MustLore("task", "decline", id, "--note", "we keep the name").Says(t, "note kept as a comment")
	if got := tasksShow(t, s.c, id); got.Status != "cancelled" || got.CloseReason != "wont_do" {
		t.Fatalf("decline: cancelled with wont_do: %+v", got)
	}
	// decline answers a proposal only: on an accepted task it is refused
	// with the cancel hint and the task stays open (INTEG-QUESTIONS-6).
	acc := tasksAdd(t, s.c, "taken on", "--commitment", "accepted")
	e := tasksJSONErr(t, s.c, "task", "decline", acc)
	if e.Error.Code != "E_REFUSED" || !strings.Contains(e.Error.Message, "is accepted, not proposed") || !strings.Contains(e.Error.Hint, "lore task cancel --id "+acc) {
		t.Fatalf("decline of an accepted task: E_REFUSED with the cancel hint: %+v", e)
	}
	if got := tasksShow(t, s.c, acc); got.Status == "cancelled" {
		t.Fatalf("a refused decline cancelled the accepted task: %+v", got)
	}
}

// Commitment levels: a proposed task waits in `task triage`, a someday
// one in `task someday`, a deferred one in `task deferred`; none shows in
// the default list until it is accepted or its date comes. accept twice
// is a no-op success; start accepts and un-defers.
func TestTasksStatus_CommitmentAndDeferLists(t *testing.T) {
	s := newTasksSolo(t)
	plain := tasksAdd(t, s.c, "committed")
	proposed := tasksAdd(t, s.c, "agent idea", "--commitment", "proposed")
	someday := tasksAdd(t, s.c, "maybe later", "--commitment", "someday")
	deferred := tasksAdd(t, s.c, "snoozed", "--defer-until", "2026-11-05")
	startMe := tasksAdd(t, s.c, "snoozed then started", "--defer-until", "in 3d")

	def := tasksList(t, s.c)
	if !tasksHas(def, plain) || tasksHas(def, proposed) || tasksHas(def, someday) || tasksHas(def, deferred) {
		t.Fatalf("the default list shows only committed, current tasks: %v", def)
	}
	s.c.MustLore("task", "triage").Says(t, proposed, "(proposed)").Never(t, someday, plain)
	s.c.MustLore("task", "someday").Says(t, someday, "(someday)").Never(t, proposed, plain)
	s.c.MustLore("task", "deferred").Says(t, deferred, "2026-11-05").Never(t, plain)
	if got := tasksShow(t, s.c, startMe); got.DeferredUntil == "" {
		t.Fatalf("--defer-until 'in 3d' is stored: %+v", got)
	}

	s.c.MustLore("task", "accept", proposed)
	s.c.MustLore("task", "accept", proposed)
	if got := tasksShow(t, s.c, proposed); got.Commitment != "accepted" || !tasksHas(tasksList(t, s.c), proposed) {
		t.Fatalf("an accepted task is committed and listed: %+v", got)
	}
	s.c.MustLore("task", "start", startMe)
	if got := tasksShow(t, s.c, startMe); got.DeferredUntil != "" || got.Status != "in_progress" {
		t.Fatalf("start un-defers: %+v", got)
	}
	s.a.SetNow(time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))
	if !tasksHas(tasksList(t, s.c), deferred) {
		t.Fatal("a deferred task is back in the list once its date has passed")
	}
	s.c.MustLore("task", "edit", someday, "--commitment", "accepted")
	if !tasksHas(tasksList(t, s.c), someday) {
		t.Fatal("a someday task edited to accepted is listed")
	}
	again := tasksAdd(t, s.c, "snooze and wake", "--defer-until", "2026-12-01")
	s.c.MustLore("task", "edit", again, "--clear-defer")
	if got := tasksShow(t, s.c, again); got.DeferredUntil != "" {
		t.Fatalf("--clear-defer clears deferred_until: %+v", got)
	}
}

// Bad values are refused with the class exit, the E_ code, the valid set
// and nothing written; unknown or malformed ids say what an id looks like.
func TestTasksStatus_BadInputRefusals(t *testing.T) {
	s := newTasksSolo(t)
	id := tasksAdd(t, s.c, "target")
	cases := []struct {
		name string
		args []string
		code string
		say  string
	}{
		{"bad status", []string{"task", "edit", id, "--status", "bogus"}, "E_INVALID_INPUT", "todo | in_progress | done | cancelled | blocked"},
		{"bad priority", []string{"task", "edit", id, "--priority", "p0"}, "E_INVALID_INPUT", "low"},
		{"bad commitment", []string{"task", "add", "x", "--commitment", "maybe"}, "E_INVALID_INPUT", "accepted | proposed | someday"},
		{"bad defer date", []string{"task", "add", "x", "--defer-until", "notadate"}, "E_DATE", "YYYY-MM-DD"},
		{"bad due date", []string{"task", "edit", id, "--due", "the 32nd"}, "E_DATE", "YYYY-MM-DD"},
		{"malformed id", []string{"task", "show", "nope"}, "E_INVALID_ID", "tsk_"},
		{"wrong kind of id", []string{"task", "start", "msn_01a1b49bd6f37ec2a25daf2e333d8be2"}, "E_INVALID_ID", "tsk_"},
		{"empty id", []string{"task", "done", ""}, "E_INVALID_INPUT", "missing required field id"},
	}
	for _, tc := range cases {
		e := tasksJSONErr(t, s.c, tc.args...)
		if e.Error.Code != tc.code || e.Error.Exit != tasksExitBadInput || !strings.Contains(e.Error.Message+e.Error.Hint, tc.say) {
			t.Fatalf("%s: want %s exit %d saying %q: %+v", tc.name, tc.code, tasksExitBadInput, tc.say, e)
		}
	}
	if got := tasksShow(t, s.c, id); got.Status != "todo" || got.Priority != "medium" || got.DueAt != "" {
		t.Fatalf("refused edits change nothing: %+v", got)
	}
	// A well-formed id that names no task is E_NOT_FOUND (exit 27).
	missing := id[:len(id)-6] + "abcdef"
	s.c.Lore("task", "show", missing).Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	s.c.Lore("task", "done", missing, "--no-lesson").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
}
