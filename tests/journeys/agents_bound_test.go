//go:build journeys && !windows

package journeys

// agents_bound_test.go — devco:D-889 (bug hunt H-122, owner 2026-11-22):
// `lore agent start` run inside a coding tool binds that tool session to
// the agent, so every later command of the session (each one a new shell,
// with no LORE_ACTOR) writes as the agent, with its LORE_SESSION, and
// whoami names the binding as the source. Another session of the tool is
// still "<person> via <tool>", LORE_HUMAN=1 and LORE_ACTOR still win,
// `lore agent stop` ends the binding where it is printed, and so does the
// tool's session-end hook (`lore agent end --tool codex`).

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyAgentStartBindsToolSession(t *testing.T) {
	t.Parallel()
	w, m, app, person, _, codexActor := jAgentsDay1(t)
	const label, session = "frontend_3", "bd-1"
	var agentActor, loreSession string

	who := func(tool *jAgentsTool, ses string, extra ...string) teamharness.Result {
		return jAgentsAs(app, tool, ses).With(extra...).Lore("whoami")
	}
	stamped := func(body string, extra ...string) string {
		r := jAgentsAs(app, &jCodex, session).With(extra...).Lore("memory", "add", "--body", body)
		m.results = append(m.results, r)
		r.OK(t)
		return jAgentsRows(t, m, app)[body].CreatedBy
	}

	w.Step("agent-start-binds-the-codex-session", func() {
		m.LoreIn(app, "actor", "register", "--yes").OK(t)
		r := jAgentsRun(m, app, &jCodex, session, "agent", "start", "--label", label)
		r.OK(t)
		lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
		last := lines[len(lines)-1]
		if !strings.HasPrefix(last, "export LORE_ACTOR=agent:"+label+" LORE_SESSION=s-") {
			t.Fatalf("agent start: the export is not the last line:\n%s", r)
		}
		loreSession = strings.TrimPrefix(last, "export LORE_ACTOR=agent:"+label+" LORE_SESSION=")
		if !strings.Contains(r.Stdout, "every new shell of this codex session now writes as agent:"+label+" (LORE_SESSION="+loreSession+") without a prefix; `lore agent stop` ends it") {
			t.Errorf("agent start inside codex does not say the session is bound:\n%s", r)
		}
	})

	w.Step("the-next-shell-of-the-session-is-the-agent", func() {
		c := jAgentsAs(app, &jCodex, session)
		got := teamharness.DecodeJSON[jAgentsWho](t, c.Lore("whoami", "--json")).Data.Identity
		if got.Kind != "agent" || got.Name != label {
			t.Fatalf("whoami in the bound session: %+v, want the agent %s", got, label)
		}
		agentActor = got.ActorID
		r := who(&jCodex, session)
		m.results = append(m.results, r)
		if !strings.Contains(r.Stdout, "session "+loreSession) || !strings.Contains(r.Stdout, "from: lore agent start in this codex session; `lore agent stop` ends it") {
			t.Errorf("whoami does not name the binding:\n%s", r)
		}
		if by := stamped("bound note one"); by != agentActor {
			t.Errorf("a note in the bound session is stamped %s, want the agent %s (person %s, detected %s)", by, agentActor, person, codexActor)
		}
	})

	w.Step("other-sessions-human-and-explicit-actor-are-not-bound", func() {
		if r := who(&jCodex, "bd-2"); !strings.Contains(r.Stdout, jAgentsName+" via codex") {
			t.Errorf("another codex session took the binding:\n%s", r)
		}
		if by := stamped("bound note as the person", "LORE_HUMAN=1"); by != person {
			t.Errorf("LORE_HUMAN=1 in the bound session is stamped %s, want the person %s", by, person)
		}
		if r := who(&jCodex, session, "LORE_ACTOR=agent:other_1"); !strings.Contains(r.Stdout, "agent:other_1") {
			t.Errorf("LORE_ACTOR lost to the binding:\n%s", r)
		}
	})

	w.Step("printed-agent-stop-ends-it", func() {
		r := jAgentsRun(m, app, &jCodex, session, "agent", "stop")
		r.OK(t)
		if !strings.Contains(r.Stdout, "this codex session no longer writes as agent:"+label) {
			t.Errorf("agent stop:\n%s", r)
		}
		if by := stamped("after stop note"); by != codexActor {
			t.Errorf("after agent stop a note is stamped %s, want %s via codex (%s)", by, jAgentsName, codexActor)
		}
		r = jAgentsRun(m, app, &jCodex, session, "agent", "stop")
		r.OK(t)
		if !strings.Contains(r.Stdout, "nothing to stop") {
			t.Errorf("a second agent stop:\n%s", r)
		}
	})

	w.Step("session-end-hook-ends-it", func() {
		jAgentsRun(m, app, &jCodex, session, "agent", "start", "--label", label).OK(t)
		if r := who(&jCodex, session); !strings.Contains(r.Stdout, "lore agent start in this codex session") {
			t.Fatalf("not bound again:\n%s", r)
		}
		jAgentsAs(app, &jCodex, session).Lore("agent", "end", "--tool", "codex").OK(t)
		if r := who(&jCodex, session); !strings.Contains(r.Stdout, jAgentsName+" via codex") {
			t.Errorf("the session-end hook left the binding:\n%s", r)
		}
	})
}
