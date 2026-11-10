package app

import (
	"regexp"
	"strings"
	"testing"
)

// fenceTaskID is a task id in lore's output.
var fenceTaskID = regexp.MustCompile(`tsk_[0-9a-f]{32}`)

// TestHeartbeatAfterCancelWithinFetchTTL (b-errs F2, TEAM F7 §7.4): a
// person cancels a held task in another clone and the holder's next
// heartbeat comes while its last fetch is still younger than
// live.fetch_ttl (the default, not the team suite's 0s). The fence must be
// explained from the remote's task row: E_CANCELLED, exit 13, never
// E_LOST_CLAIM from the stale local row. A revoke still answers
// E_LOST_CLAIM (the fresh row is not cancelled). A claim of a task added
// after the last fetch refetches too (B-ERRS2-1).
func TestHeartbeatAfterCancelWithinFetchTTL(t *testing.T) {
	t.Parallel()
	// Every case below is its own world (remote, the person's clone, a
	// fresh holder clone), so the cases run in parallel; each keeps the
	// order the fence depends on: the holder's previous command fetched,
	// the person then adds or changes the task, the holder's next command
	// comes within live.fetch_ttl of that fetch.
	for _, tc := range []struct {
		human []string
		code  int
		want  string
	}{
		{[]string{"task", "cancel", "--yes"}, 13, "E_CANCELLED"},
		{[]string{"task", "revoke"}, 12, "E_LOST_CLAIM"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			w, person, holder := fenceWorld(t)
			code, out := w.loreOut(t, person, "task", "add", "held work "+tc.want)
			id := fenceTaskID.FindString(out)
			if code != 0 || id == "" {
				t.Fatalf("task add: %d %s", code, out)
			}
			// A fresh holder clone: its first command fetches, so the task
			// is there; the heartbeat below is within fetch_ttl of it.
			if code, out = w.loreOut(t, holder, "task", "claim", id); code != 0 {
				t.Fatalf("claim: %d %s", code, out)
			}
			if code, out = w.loreOut(t, person, append(append([]string{}, tc.human[:2]...), append([]string{id}, tc.human[2:]...)...)...); code != 0 {
				t.Fatalf("%v: %d %s", tc.human, code, out)
			}
			code, out = w.loreOut(t, holder, "task", "heartbeat", id)
			if code != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("heartbeat after %v: exit %d %s, want %d %s", tc.human[:2], code, out, tc.code, tc.want)
			}
		})
	}

	// A task a teammate adds after the holder's last fetch (within
	// fetch_ttl) is claimed, not E_NOT_FOUND (B-ERRS2-1). A well-formed id
	// nobody added stays E_NOT_FOUND after the one refetch.
	t.Run("claim and note after the last fetch", func(t *testing.T) {
		t.Parallel()
		w, person, holder := fenceWorld(t)
		// the holder holds nothing yet (max_claims_per_agent 1): it reads
		// the person's task, which fetches
		fenceFetched(t, w, person, holder, "show")
		code, out := w.loreOut(t, person, "task", "add", "added after the holder fetched")
		id := fenceTaskID.FindString(out)
		if code != 0 || id == "" {
			t.Fatalf("task add: %d %s", code, out)
		}
		if code, out = w.loreOut(t, holder, "task", "claim", id); code != 0 || !strings.Contains(out, "claimed") {
			t.Fatalf("claim of a task added after the last fetch: exit %d %s", code, out)
		}
		// The same holds for the other coordination ops on a task id
		// (B-ERRS2-2): a note on a task added after the claim's refetch,
		// still within fetch_ttl, lands instead of E_NOT_FOUND.
		code, out = w.loreOut(t, person, "task", "add", "added after the claim")
		if id = fenceTaskID.FindString(out); code != 0 || id == "" {
			t.Fatalf("task add: %d %s", code, out)
		}
		if code, out = w.loreOut(t, holder, "task", "note", id, "progress"); code != 0 || !strings.Contains(out, "note") {
			t.Fatalf("note on a task added after the last fetch: exit %d %s", code, out)
		}
		if code, out = w.loreOut(t, holder, "task", "claim", "tsk_01a1a681c07e7dfe8ec01fc44d487e86"); code != 27 || !strings.Contains(out, "E_NOT_FOUND") {
			t.Fatalf("claim of an unknown id: exit %d %s, want 27 E_NOT_FOUND", code, out)
		}
	})

	// Every other operation on a task id too: show, done, cancel
	// (B-ERRS2-3) and edit, start, watch, unwatch, history, checklist, time,
	// delete (B-ERRS2-4), each on a task added after the holder's previous
	// command, still within fetch_ttl.
	for _, c := range []struct{ verb, rest []string }{
		{[]string{"show"}, nil}, {[]string{"done"}, nil}, {[]string{"cancel"}, []string{"--yes"}},
		{[]string{"edit"}, []string{"--title", "renamed"}}, {[]string{"start"}, nil},
		{[]string{"watch"}, nil}, {[]string{"unwatch"}, nil}, {[]string{"history"}, nil},
		{[]string{"checklist", "add"}, []string{"item one"}}, {[]string{"time", "log"}, []string{"5m"}},
		{[]string{"delete"}, []string{"--yes"}},
	} {
		t.Run(strings.Join(c.verb, " "), func(t *testing.T) {
			t.Parallel()
			w, person, holder := fenceWorld(t)
			// the holder holds a claim, as in the person's real flow
			fenceFetched(t, w, person, holder, "claim")
			code, out := w.loreOut(t, person, "task", "add", "added before "+strings.Join(c.verb, " "))
			id := fenceTaskID.FindString(out)
			if code != 0 || id == "" {
				t.Fatalf("task add: %d %s", code, out)
			}
			args := append(append(append([]string{"task"}, c.verb...), id), c.rest...)
			if code, out = w.loreOut(t, holder, args...); code != 0 {
				t.Fatalf("task %v of a task added after the last fetch: exit %d %s", c.verb, code, out)
			}
		})
	}
}

// fenceWorld is a person's pushed lore repo and a holder's fresh clone of
// it, both run as the same person (one HOME, as two clones of one user).
func fenceWorld(t *testing.T) (w *cliWorld, person, holder string) {
	t.Helper()
	w = newCLIWorld(t).person().author()
	person, holder = w.livePair(t)
	return w, person, holder
}

// fenceFetched makes the holder's next command come after a fetch: the
// person adds a task and the holder runs `task <verb>` on it (a fresh
// clone's first command fetches; the task is only on the remote, so the
// command cannot succeed without one), so whatever the person adds next is
// newer than the holder's last fetch.
func fenceFetched(t *testing.T, w *cliWorld, person, holder, verb string) {
	t.Helper()
	code, out := w.loreOut(t, person, "task", "add", "held work")
	id := fenceTaskID.FindString(out)
	if code != 0 || id == "" {
		t.Fatalf("task add: %d %s", code, out)
	}
	if code, out = w.loreOut(t, holder, "task", verb, id); code != 0 || !strings.Contains(out, id) {
		t.Fatalf("task %s in the holder's clone: %d %s", verb, code, out)
	}
}
