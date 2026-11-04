package toolprofile

// Session hooks (AGENT_IDENTITY_DESIGN 6.4, OQ8): the optional per-tool
// snippet a tool runs at session start, `lore agent start --tool <slug>
// --hook-stdin`, so lore learns what env vars miss (mostly the model).
//
// TOOL NEUTRAL: the hooks are a table keyed by profile name like the
// profiles themselves; nothing outside this file names a tool's hook
// file. A tool gets a row ONLY when its hook input names are confirmed
// from the vendor's docs or source (the detector research rule); every
// other tool answers "no confirmed session hook" and nothing is
// installed.
//
//	SessionHookFor(tool) ─┬─ row  → setup --agent-hooks writes Command into ConfigFile under Event
//	                      │         agent start --hook-stdin decodes stdin with Parse
//	                      └─ none → "no confirmed session hook"

import (
	"encoding/json"
	"strings"
)

// HookInput is what a tool's hook stdin told lore; "" each when absent.
type HookInput struct {
	SessionID string
	Model     string
}

// SessionHook is one tool's confirmed session hook.
type SessionHook struct {
	// Tool is the profile / detector slug.
	Tool string
	// ConfigFile is the repo-relative settings file the snippet goes in.
	ConfigFile string
	// Event is the tool's session-start hook event name.
	Event string
	// Source documents where the input names were confirmed.
	Source string
	// Parse reads the tool's hook JSON from stdin.
	Parse func([]byte) (HookInput, error)
}

// Command is the snippet's command line (6.4).
func (h SessionHook) Command() string { return HookCommand(h.Tool) }

// HookCommand is `lore agent start --tool <tool> --hook-stdin`.
func HookCommand(tool string) string {
	return "lore agent start --tool " + tool + " --hook-stdin"
}

// claudeHookInput is Claude Code's hook stdin: session_id is common to
// every event, model is sent only on SessionStart and not always
// (code.claude.com/docs/en/hooks, read 2026-11-04).
type claudeHookInput struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
}

func parseClaudeHook(b []byte) (HookInput, error) {
	var in claudeHookInput
	if err := json.Unmarshal(b, &in); err != nil {
		return HookInput{}, err
	}
	return HookInput{SessionID: strings.TrimSpace(in.SessionID), Model: strings.TrimSpace(in.Model)}, nil
}

// SessionHooks lists the confirmed session hooks in a fixed order.
func SessionHooks() []SessionHook {
	return []SessionHook{
		{Tool: NameClaudeCode, ConfigFile: ".claude/settings.json", Event: "SessionStart",
			Source: "https://code.claude.com/docs/en/hooks", Parse: parseClaudeHook},
	}
}

// SessionHookFor returns tool's confirmed hook.
func SessionHookFor(tool string) (SessionHook, bool) {
	for _, h := range SessionHooks() {
		if h.Tool == tool {
			return h, true
		}
	}
	return SessionHook{}, false
}
