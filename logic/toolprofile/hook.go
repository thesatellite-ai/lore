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
//	SessionEndHookFor(tool) ─ row → setup --agent-hooks writes `lore agent end --tool <slug>`
//	                                under the tool's session-end event, beside the start hook
//
// The session-end hook (decided 2026-11-10, land7 A; it replaces PUSH-C7's
// "no session-end hook") pushes the store batches still waiting when the
// session closes. Every tool with a start-hook row has a confirmed
// session-end event, so each gets one; the hook always exits 0, prints
// nothing and is bounded by the piggyback limit (cmd/lore sessionend.go).

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
	// Parse reads the tool's hook JSON from stdin (session hooks only).
	Parse func([]byte) (HookInput, error)
	// Matcher limits the hook to some tool calls ("Read|Edit|Write"); ""
	// for every call.
	Matcher string
	// Cmd overrides the command line (the context and end hooks); ""
	// means HookCommand(Tool).
	Cmd string
	// Timeout is the hook entry's "timeout" in seconds (0 = the tool's
	// default, no key written). Claude Code gives SessionEnd hooks 1.5 s
	// in all unless a hook asks for more, which the end hook's push needs.
	Timeout int
}

// Command is the snippet's command line (6.4).
func (h SessionHook) Command() string {
	if h.Cmd != "" {
		return h.Cmd
	}
	return HookCommand(h.Tool)
}

// HookCommand is `lore agent start --tool <tool> --hook-stdin`.
func HookCommand(tool string) string {
	return "lore agent start --tool " + tool + " --hook-stdin"
}

// EndHookCommand is `lore agent end --tool <tool>`: the session-end hook.
// It reads no stdin (nothing in the end event is needed, and an unread
// pipe can never hold it up).
func EndHookCommand(tool string) string {
	return "lore agent end --tool " + tool
}

// EndHookTimeout is the Claude Code SessionEnd entry's timeout (seconds):
// above lore's own bound on the hook (the 15 s piggyback limit plus its
// stop margin), so the tool never cuts a push off before lore does.
const EndHookTimeout = 30

// SessionEndHooks lists the confirmed session-end hooks, one per tool with
// a session hook, in the same order. Sources, read 2026-11-10: Claude Code
// SessionEnd (cannot block; a 1.5 s budget raised by a hook's timeout, up
// to 60 s), Codex SessionEnd ("run a command when a session ends"), Gemini
// CLI SessionEnd (fires on exit; the CLI does not wait for it).
func SessionEndHooks() []SessionHook {
	return []SessionHook{
		{Tool: NameClaudeCode, ConfigFile: ".claude/settings.json", Event: EventSessionEnd,
			Source: "https://code.claude.com/docs/en/hooks", Cmd: EndHookCommand(NameClaudeCode), Timeout: EndHookTimeout},
		{Tool: NameCodex, ConfigFile: codexHookFile, Event: EventSessionEnd,
			Source: codexHookSource, Cmd: EndHookCommand(NameCodex)},
		{Tool: NameGeminiCLI, ConfigFile: geminiHookFile, Event: EventSessionEnd,
			Source: geminiHookSource, Cmd: EndHookCommand(NameGeminiCLI)},
	}
}

// EventSessionEnd is the session-end event name; the three tools spell it
// the same.
const EventSessionEnd = "SessionEnd"

// SessionEndHookFor returns tool's confirmed session-end hook.
func SessionEndHookFor(tool string) (SessionHook, bool) {
	for _, h := range SessionEndHooks() {
		if h.Tool == tool {
			return h, true
		}
	}
	return SessionHook{}, false
}

// claudeHookInput is the hook stdin shared by Claude Code, Codex and Gemini
// CLI: session_id is common to every event; model is sent by Claude Code on
// SessionStart (not always) and by Codex on every event, never by Gemini CLI
// (code.claude.com/docs/en/hooks read 2026-11-04; learn.chatgpt.com/docs/hooks
// and geminicli.com/docs/hooks/reference read 2026-11-05).
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
		// Codex: same hooks JSON as Claude Code in <repo>/.codex/hooks.json;
		// a project hook runs only after the person trusts it (/hooks), so a
		// teammate without lore is never run against it (FA-CTX-5).
		{Tool: NameCodex, ConfigFile: codexHookFile, Event: "SessionStart",
			Source: codexHookSource, Parse: parseClaudeHook},
		// Gemini CLI: the hooks key of the project .gemini/settings.json.
		{Tool: NameGeminiCLI, ConfigFile: geminiHookFile, Event: "SessionStart",
			Source: geminiHookSource, Parse: parseClaudeHook},
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

// Hook files and sources of the tools beyond Claude Code (OV18-5 lifted by
// FA-CTX-5 after reading each tool's hook reference).
const (
	codexHookFile    = ".codex/hooks.json"
	codexHookSource  = "https://learn.chatgpt.com/docs/hooks"
	geminiHookFile   = ".gemini/settings.json"
	geminiHookSource = "https://geminicli.com/docs/hooks/reference"
)

// ContextHooks are the context hooks (CONTEXT_BUDGET 6.9): `lore context --hook <tool>
// --hook-stdin` on each file the agent reads or edits (always installed
// with the hooks), and on each prompt only when promptSearch is on (CQ5,
// context.hooks.prompt_search). Claude Code's go in its LOCAL settings file,
// so a teammate without lore is not affected; Codex and Gemini CLI have no
// local file, so theirs go in the project hook file (FA-CTX-5). Only tools
// whose hook output field (additionalContext) is confirmed get a row.
func ContextHooks(promptSearch bool) []SessionHook {
	const src = "https://code.claude.com/docs/en/hooks"
	cmd := "lore context --hook " + NameClaudeCode + " --hook-stdin"
	out := []SessionHook{{Tool: NameClaudeCode, ConfigFile: ".claude/settings.local.json", Event: "PreToolUse",
		Matcher: "Read|Edit|Write", Cmd: cmd, Source: src},
		// CONTEXT_BUDGET 6.9: the SessionStart context hook.
		{Tool: NameClaudeCode, ConfigFile: ".claude/settings.local.json", Event: "SessionStart",
			Cmd: cmd + " --session-start", Source: src}}
	if promptSearch {
		out = append(out, SessionHook{Tool: NameClaudeCode, ConfigFile: ".claude/settings.local.json",
			Event: "UserPromptSubmit", Cmd: cmd, Source: src})
	}
	// Codex (FA-CTX-5): PreToolUse on apply_patch (tool_input.command holds
	// the patch; lore reads its file headers) and SessionStart, both answered
	// with hookSpecificOutput.additionalContext; Codex has no file-read tool
	// (reads go through Bash), so there is no read hook.
	cx := "lore context --hook " + NameCodex + " --hook-stdin"
	out = append(out,
		SessionHook{Tool: NameCodex, ConfigFile: codexHookFile, Event: "PreToolUse", Matcher: "apply_patch", Cmd: cx, Source: codexHookSource},
		SessionHook{Tool: NameCodex, ConfigFile: codexHookFile, Event: "SessionStart", Cmd: cx + " --session-start", Source: codexHookSource})
	if promptSearch {
		out = append(out, SessionHook{Tool: NameCodex, ConfigFile: codexHookFile, Event: "UserPromptSubmit", Cmd: cx, Source: codexHookSource})
	}
	// Gemini CLI (FA-CTX-5): AfterTool on its file tools (tool_input.file_path;
	// additionalContext is appended to the tool result; BeforeTool takes no
	// additionalContext) and SessionStart. No prompt hook: BeforeAgent's
	// input field is not confirmed.
	gm := "lore context --hook " + NameGeminiCLI + " --hook-stdin"
	out = append(out,
		SessionHook{Tool: NameGeminiCLI, ConfigFile: geminiHookFile, Event: "AfterTool", Matcher: "read_file|write_file|replace", Cmd: gm, Source: geminiHookSource},
		SessionHook{Tool: NameGeminiCLI, ConfigFile: geminiHookFile, Event: "SessionStart", Cmd: gm + " --session-start", Source: geminiHookSource})
	return out
}

// ContextHooksFor returns tool's context hooks.
func ContextHooksFor(tool string, promptSearch bool) []SessionHook {
	var out []SessionHook
	for _, h := range ContextHooks(promptSearch) {
		if h.Tool == tool {
			out = append(out, h)
		}
	}
	return out
}
