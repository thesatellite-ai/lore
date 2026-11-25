package identity

// agentdisplay.go — how much of a detected agent's coding tool reaches the
// files lore writes (issue #113, devco:D-900). A detected agent's actor row
// (display_name) and `lore migrate run`'s migrated_via in _meta.json used
// to name the tool always. No git commit names the agent at all (no author,
// committer or trailer, devco:D-983): commits are the person's. Some owners allow no tool attribution in repo content, so the
// prose part is a setting decided BEFORE anything is written:
//
//	tool    "<person> via <tool>"   (default, today's text)
//	agent   "<person> (agent)"      an agent wrote it; the tool is not named
//	person  "<person>"              the person's own name
//
// The stable key (agent:auto/<tool>/<hex>) keeps the tool slug in every
// mode: it is an id the actor id is hashed from, never written into a row
// (the row carries only the hash), and changing it would split every
// existing agent's history into a new actor. What changes is the prose:
// display_name and migrated_via. An agent named on purpose
// (LORE_ACTOR=agent:<label>, `lore agent start --label`) keeps its label:
// the person chose that text.

import "strings"

// AgentDisplay is the identity.agent_display setting (policy key
// identity.agent_display, env LORE_AGENT_DISPLAY).
type AgentDisplay string

// The three modes, strictest first (policy ShapeRank order).
const (
	// AgentDisplayPerson writes the person's own name.
	AgentDisplayPerson AgentDisplay = "person"
	// AgentDisplayAgent writes "<person> (agent)".
	AgentDisplayAgent AgentDisplay = "agent"
	// AgentDisplayTool writes "<person> via <tool>" (the default).
	AgentDisplayTool AgentDisplay = "tool"
)

// EnvAgentDisplay sets the mode for one shell or one command. It only
// tightens: a repo's policy of person is never loosened by it (stricter
// wins, as for every policy). It takes a mode, not a name, so the env and
// the policy key share one closed set of values.
const EnvAgentDisplay = "LORE_AGENT_DISPLAY"

// agentSuffix is what AgentDisplayAgent appends to the person's name.
const agentSuffix = " (agent)"

// AgentDisplays is every mode, strictest first (the policy key's choices).
func AgentDisplays() []AgentDisplay {
	return []AgentDisplay{AgentDisplayPerson, AgentDisplayAgent, AgentDisplayTool}
}

// ParseAgentDisplay reads a mode; ok is false for anything outside the set
// (the caller says so and keeps the default).
func ParseAgentDisplay(s string) (AgentDisplay, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, m := range AgentDisplays() {
		if s == string(m) {
			return m, true
		}
	}
	return "", false
}

// rank orders the modes, strictest (person) lowest; the zero value ranks
// as the default (tool).
func (m AgentDisplay) rank() int {
	switch m {
	case AgentDisplayPerson:
		return 0
	case AgentDisplayAgent:
		return 1
	default:
		return 2
	}
}

// StricterAgentDisplay is the mode of a and b that hides more (stricter
// wins, as for every policy).
func StricterAgentDisplay(a, b AgentDisplay) AgentDisplay {
	if b.rank() < a.rank() {
		return b
	}
	return a
}

// WithAgentDisplay is r under mode when mode hides more than r's own: a
// detected agent's display name made again from its owner and tool. The
// stable key, and so the actor id, never change. Anyone else is returned
// as is.
func WithAgentDisplay(r Resolved, mode AgentDisplay) Resolved {
	if r.Step != StepDetected || r.Owner == nil || r.Detection == nil {
		return r
	}
	m := StricterAgentDisplay(r.AgentDisplay, mode)
	if m == r.AgentDisplay {
		return r
	}
	r.AgentDisplay = m
	r.DisplayName = agentDisplayName(r.Owner.DisplayName, r.Detection.Tool, m)
	return r
}

// HidesTool reports whether the mode keeps the tool out of written prose.
// The zero value is the default (tool), which names it.
func (m AgentDisplay) HidesTool() bool {
	return m == AgentDisplayPerson || m == AgentDisplayAgent
}

// HidesTool reports whether r is a detected agent whose mode keeps the
// tool out of written prose (display_name, migrated_via).
func (r Resolved) HidesTool() bool {
	return r.Step == StepDetected && r.AgentDisplay.HidesTool()
}

// agentDisplayName is the display_name of person's detected agent in tool
// under mode.
func agentDisplayName(person, tool string, mode AgentDisplay) string {
	switch mode {
	case AgentDisplayPerson:
		return person
	case AgentDisplayAgent:
		return person + agentSuffix
	default:
		return person + " via " + tool
	}
}
