//go:build stories && !windows

package stories

// WL_ownlease_test.go — devco:D-896 (public issue #121, bug hunt H-155):
// an agent's own claim lease ran out while nobody else claimed the task.
// `task claim --push` said "you hold it already", exited 0 and renewed
// nothing; `task heartbeat` and `task done` failed E_LOST_CLAIM naming the
// agent itself as the holder, with a hint to stop work. The story replays
// the issue's script in a solo repo: each of claim, heartbeat, `task next
// --claim` and done now works on the lapsed lease (claim and next renew
// it); once another agent reclaimed the task after the grace, the first
// agent is fenced out as before and the hint still sends it to other work.

import (
	"strings"
	"testing"
	"time"
)

// wlLapse is past the default lease (20m) and inside its grace (2m): the
// shape of the issue (lease.ttl 15s, 25s asleep).
const wlLapse = 21 * time.Minute

// wlPastGrace is past the default lease, grace and renew_every watch: a
// reader may treat the claim as expired and reclaim it.
const wlPastGrace = 30 * time.Minute

func TestStoryWLOwnLapsedLease(t *testing.T) {
	s := New(t, "WL")
	m, h := blSolo(s)
	m.LoreIn(h, "init").OK(t)
	h.CommitAll("lore init")
	h.Git("push", "-q", "origin", "HEAD:main")
	agentEnv := func(label string) []string {
		return []string{"LORE_HUMAN=", "LORE_ACTOR=agent:test/" + label}
	}
	w1 := h.With(agentEnv("worker-1")...)
	w2 := h.With(agentEnv("worker-2")...)
	add := func(title string) string {
		return idIn(t, blTaskID, w1.Lore("task", "add", "--title", title, "--commitment", "accepted").OK(t))
	}
	head := strings.TrimSpace(h.Git("rev-parse", "HEAD"))

	s.Step("D-896/claim-renews-my-lapsed-lease", func() {
		id := add("repro task")
		w1.Lore("task", "claim", "--id", id, "--push").OK(t)
		s.Advance(wlLapse)
		r := w1.Lore("task", "claim", "--id", id, "--push").OK(t)
		r.Says(t, "renewed your claim on "+id, "nobody took the task").Never(t, "you hold "+id+" already (until")
		s.Advance(wlLapse)
		w1.Lore("task", "heartbeat", "--id", id).OK(t).Says(t, id+" renewed until")
		s.Advance(wlLapse)
		w1.Lore("task", "done", "--id", id, "--commit", head, "--no-lesson").OK(t).Says(t, id+" done")
	})

	s.Step("D-896/heartbeat-and-done-accept-my-lapsed-lease", func() {
		id := add("heartbeat after a lapse")
		w1.Lore("task", "claim", "--id", id, "--push").OK(t)
		s.Advance(wlLapse)
		w1.Lore("task", "heartbeat", "--id", id).OK(t).Says(t, id+" renewed until").Never(t, "E_LOST_CLAIM")
		s.Advance(wlLapse)
		w1.Lore("task", "done", "--id", id, "--commit", head, "--no-lesson").OK(t).Never(t, "E_LOST_CLAIM")
	})

	s.Step("D-896/next-claim-renews-the-lapsed-lease-it-resumes", func() {
		id := add("next resumes after a lapse")
		w1.Lore("task", "claim", "--id", id, "--push").OK(t)
		s.Advance(wlLapse)
		w1.Lore("task", "next", "--claim", "--push").OK(t).Says(t, "renewed your claim on "+id, "resume, or `lore task release --id "+id+"`")
		s.Advance(wlLapse)
		w1.Lore("task", "done", "--id", id, "--commit", head, "--no-lesson").OK(t)
	})

	s.Step("D-896/reclaimed-by-another-agent-stays-fenced", func() {
		id := add("taken over after the grace")
		w1.Lore("task", "claim", "--id", id, "--push").OK(t)
		w2.Lore("task", "list").OK(t) // worker-2's clone watches the claim from here
		s.Advance(wlPastGrace)
		w2.Lore("task", "claim", "--id", id, "--push").OK(t).Says(t, "reclaim, attempt 2")
		w1.Lore("task", "heartbeat", "--id", id).Fails(t, 12, "E_LOST_CLAIM", "agent:test/worker-2", "stop work on this task")
		w1.Lore("task", "done", "--id", id, "--commit", head, "--no-lesson").Fails(t, 12, "E_LOST_CLAIM", "agent:test/worker-2")
		w1.Lore("task", "claim", "--id", id, "--push").Refused(t, "held by agent:test/worker-2")
		w2.Lore("task", "done", "--id", id, "--commit", head, "--no-lesson").OK(t)
	})
}
