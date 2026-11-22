//go:build journeys && !windows

package journeys

// agents_explicit_test.go — devco:D-850: inside a coding tool's session an
// explicit LORE_ACTOR=agent:<label> names who writes; the detected tool
// (and LORE_HUMAN=1) never take over. `lore agent start` run inside a tool
// says its export reaches no later command and prints the prefix that
// does (each command of a tool runs in a new shell).

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// jCodexNoSession is a codex session whose environment names no session
// id (CODEX_SANDBOX only).
var jCodexNoSession = jAgentsTool{slug: jAgentsCodex, env: func(string) []string { return []string{"CODEX_SANDBOX=seatbelt"} }}

func TestJourneyExplicitActorInAgentSession(t *testing.T) {
	t.Parallel()
	w, m, app, person, claudeActor, codexActor := jAgentsDay1(t)
	const label = "frontend_2"
	explicit := "LORE_ACTOR=agent:" + label

	w.Step("explicit-actor-wins-in-each-tool", func() {
		var labelActor string
		for _, tc := range []struct {
			tool     *jAgentsTool
			detected string
			extra    []string
		}{
			{&jClaude, claudeActor, nil},
			{&jCodex, codexActor, nil},
			{&jCodex, codexActor, []string{"LORE_HUMAN=1"}},
		} {
			c := jAgentsAs(app, tc.tool, "ex-1").With(append([]string{explicit}, tc.extra...)...)
			who := teamharness.DecodeJSON[jAgentsWho](t, c.Lore("whoami", "--json")).Data.Identity
			if who.Kind != "agent" || who.Name != label {
				t.Errorf("whoami in a %s session with %s %v: %+v, want the agent %s", tc.tool.slug, explicit, tc.extra, who, label)
			}
			body := strings.TrimSpace("explicit note via " + tc.tool.slug + " " + strings.Join(tc.extra, " "))
			r := c.Lore("memory", "add", "--body", body)
			m.results = append(m.results, r)
			r.OK(t)
			got := jAgentsRows(t, m, app)[body].CreatedBy
			if got == tc.detected || got == person || got != who.ActorID {
				t.Errorf("%q in a %s session is stamped %s (detected agent %s, person %s), want %s's %s", body, tc.tool.slug, got, tc.detected, person, label, who.ActorID)
			}
			if labelActor == "" {
				labelActor = got
			} else if got != labelActor {
				t.Errorf("%s writes as two actors: %s and %s", label, labelActor, got)
			}
		}
	})

	w.Step("agent-start-inside-a-tool-prints-the-prefix", func() {
		m.LoreIn(app, "actor", "register", "--yes").OK(t) // agent start registers under a person on the roster
		// a codex that names no session (only CODEX_SANDBOX): nothing to bind
		// to (devco:D-889 binds a tool session that has an id), so the prefix
		r := jAgentsRun(m, app, &jCodexNoSession, "", "agent", "start", "--label", label)
		r.OK(t)
		lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
		if len(lines) < 2 || !strings.HasPrefix(lines[len(lines)-1], "export LORE_ACTOR=agent:"+label+" LORE_SESSION=") {
			t.Fatalf("agent start: the export is not the last line:\n%s", r)
		}
		prefix := strings.TrimPrefix(lines[len(lines)-1], "export ")
		if !strings.Contains(r.Stdout, "codex runs each command in a new shell") || !strings.Contains(r.Stdout, "start every lore command of this agent with "+prefix) {
			t.Errorf("agent start inside codex does not say the export stays in its shell:\n%s", r)
		}
		// outside a tool the output is the two lines it always was
		r = jAgentsRun(m, app, nil, "", "agent", "start", "--label", label)
		r.OK(t)
		if strings.Contains(r.Stdout, "new shell") || strings.Count(strings.TrimSpace(r.Stdout), "\n") != 1 {
			t.Errorf("agent start at the person's terminal:\n%s", r)
		}
	})
}
