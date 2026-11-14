//go:build journeys && !windows

package journeys

import (
	"regexp"
	"testing"
)

// jGateID finds the first id with prefix p ("msn", "tsk") in out.
func jGateID(t *testing.T, p, out string) string {
	t.Helper()
	id := regexp.MustCompile(`\b` + p + `_[0-9a-f]{32}\b`).FindString(out)
	if id == "" {
		t.Fatalf("no %s_ id in:\n%s", p, out)
	}
	return id
}

// TestJourneyAgentsGate: a person's detected coding agent acts for the
// person at a gate (agent-policy round 2, findings 7 and 8). Ada puts a gate
// on a mission task; in her Codex session the inbox lists the gate (it
// was hidden from a detected agent) and `lore gate approve` approves it as
// "Ada via codex" (it was refused: "only a person approves a gate"); the
// task is then no longer gated.
func TestJourneyAgentsGate(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona(jAgentsName, jAgentsEmail)
	m := ada.Laptop()
	jAgentsIdentity(t, m)
	app := m.Clone(w.Remote(jAgentsRepo), "app")
	var task string

	w.Step("day1/gate-on-a-mission-task", func() {
		m.LoreIn(app, "init").OK(t)
		mission := jGateID(t, "msn", m.LoreIn(app, "mission", "add", "--title", "ship the release").OK(t).Out())
		task = jGateID(t, "tsk", m.LoreIn(app, "task", "add", "--title", "deploy to staging", "--mission", mission).OK(t).Out())
		m.LoreIn(app, "gate", "request", "--id", task).OK(t)
	})

	w.Step("day1/codex-sees-and-approves-the-gate", func() {
		codex := jAgentsAs(app, &jCodex, "cx-gate").With("LORE_LIVE_AUTOPUSH=1")
		r := codex.Lore("inbox")
		m.results = append(m.results, r)
		r.OK(t).Says(t, task, "gate needs approval")
		r = codex.Lore("gate", "approve", "--id", task)
		m.results = append(m.results, r)
		r.OK(t).Says(t, "approved by "+jAgentsName+" via "+jAgentsCodex).Never(t, "only a person approves")
		r = codex.Lore("inbox")
		m.results = append(m.results, r)
		r.OK(t).Never(t, "gate needs approval")
	})
}
