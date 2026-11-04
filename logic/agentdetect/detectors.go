package agentdetect

// Confidence is how well a detector row is backed (section 4).
type Confidence string

// The confidence levels.
const (
	High    Confidence = "high"
	Medium  Confidence = "medium"
	Low     Confidence = "low"
	Unknown Confidence = "unknown"
)

// Match is how a Signal compares an environment variable.
type Match int

// The match kinds.
const (
	// Present: the variable is set to any non-empty value.
	Present Match = iota
	// Equals: the value equals Signal.Value.
	Equals
	// Prefix: the value starts with Signal.Value.
	Prefix
	// Contains: the value contains Signal.Value.
	Contains
	// BothWith: the variable and Signal.Value (another name) are both set.
	BothWith
)

// Signal is one environment test.
type Signal struct {
	Env   string
	Match Match
	Value string
}

// ProcMatch matches one ancestor process: Exe is the executable base name
// (lower case, no .exe); with Script set, Exe is a launcher (node, python)
// and Script the base name of its first script argument.
type ProcMatch struct {
	Exe    string
	Script string
}

// Detector describes one coding tool. A built-in tool is a code change and
// a release, by design (R11 B+): there is no user config. Plugins may pass
// extra Detectors on top (PQ1); they never replace a built-in one.
type Detector struct {
	// Tool is the snake_case slug stored in actors.label and sessions.tool.
	Tool string
	// Strong: any match means "this tool ran the command".
	Strong []Signal
	// Weak: the tool's terminal is open; decides only without a TTY (OQ11).
	Weak []Signal
	// AgentNames are AI_AGENT / AGENT values naming this tool, as written.
	AgentNames []string
	// SessionEnv: first non-empty wins.
	SessionEnv []string
	// ModelEnv are user-set config variables (recorded "from config").
	ModelEnv []string
	// Procs are the ancestor-walk matches.
	Procs []ProcMatch
	// Source backs the strong signals (URL or repo path).
	Source string
	// Confidence of the row.
	Confidence Confidence
}

// UnknownAgent is the tool of a generic AGENT=1 / AI_AGENT=<unknown> (OQ15).
const UnknownAgent = "unknown_agent"

// Shared convention variables (section 3 step 3.2).
const (
	EnvAIAgent = "AI_AGENT"
	EnvAgent   = "AGENT"
)

// launchers run a tool as a script; the walk then matches the script name.
var launchers = []string{"node", "python", "python3", "bun", "deno"}

// p is the Present shorthand; eq the Equals shorthand.
func p(env string) Signal     { return Signal{Env: env, Match: Present} }
func eq(env, v string) Signal { return Signal{Env: env, Match: Equals, Value: v} }

// Builtin returns the built-in table in priority order (section 4). High
// rows are strong; Medium, Low and Unknown rows keep their markers as weak
// signals plus their process name until a live capture confirms them
// (section 4, "What ships in 0.1.18"), except where a Medium row's marker
// is the tool's own agent-only variable (CURSOR_AGENT, Kiro's FIFO pair).
// Qwen Code is listed before Gemini CLI: it is a fork and may carry both.
func Builtin() []Detector {
	return []Detector{
		{Tool: "claude_code", Strong: []Signal{eq("CLAUDE_CODE_CHILD_SESSION", "1")}, Weak: []Signal{eq("CLAUDECODE", "1")},
			AgentNames: []string{"claude-code", "claude_code"}, SessionEnv: []string{"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_REMOTE_SESSION_ID"},
			Procs: []ProcMatch{{Exe: "claude"}}, Source: "code.claude.com/docs/en/env-vars", Confidence: High},
		{Tool: "codex", Strong: []Signal{p("CODEX_THREAD_ID"), p("CODEX_SANDBOX"), p("CODEX_SANDBOX_NETWORK_DISABLED"), eq("CODEX_CI", "1")},
			SessionEnv: []string{"CODEX_SESSION_ID", "CODEX_THREAD_ID"}, Procs: []ProcMatch{{Exe: "codex"}},
			Source: "openai/codex codex-rs/protocol/src/shell_environment.rs", Confidence: High},
		{Tool: "opencode", Strong: []Signal{eq("OPENCODE", "1")}, AgentNames: []string{"opencode"}, SessionEnv: []string{"OPENCODE_SESSION_ID"},
			Procs: []ProcMatch{{Exe: "opencode"}}, Source: "anomalyco/opencode packages/core/src/tool/plugin/shell.ts", Confidence: High},
		{Tool: "qwen_code", Strong: []Signal{eq("QWEN_CODE", "1")}, Procs: []ProcMatch{{Exe: "node", Script: "qwen"}, {Exe: "qwen"}},
			Source: "QwenLM/qwen-code packages/core/src/services/shellExecutionService.ts", Confidence: High},
		{Tool: "gemini_cli", Strong: []Signal{eq("GEMINI_CLI", "1")}, Procs: []ProcMatch{{Exe: "node", Script: "gemini"}, {Exe: "gemini"}},
			Source: "google-gemini/gemini-cli packages/core/src/services/shellExecutionService.ts", Confidence: High},
		{Tool: "cursor", Strong: []Signal{eq("CURSOR_AGENT", "1"), eq("CURSOR_EXTENSION_HOST_ROLE", "agent-exec")}, Weak: []Signal{p("CURSOR_TRACE_ID"), p("CURSOR_SANDBOX")},
			Procs: []ProcMatch{{Exe: "cursor-agent"}}, Source: "forum.cursor.com (staff: CURSOR_AGENT re-added)", Confidence: Medium},
		{Tool: "copilot_cli", Strong: []Signal{eq("COPILOT_CLI", "1")}, AgentNames: []string{"github-copilot-cli"}, SessionEnv: []string{"COPILOT_AGENT_SESSION_ID"},
			ModelEnv: []string{"COPILOT_MODEL"}, Procs: []ProcMatch{{Exe: "node", Script: "copilot"}, {Exe: "copilot"}},
			Source: "github/copilot-cli changelog.md", Confidence: High},
		{Tool: "copilot_vscode", AgentNames: []string{"github_copilot_vscode_agent"},
			Source: "microsoft/vscode src/vs/platform/chat/common/aiAgentEnv.ts", Confidence: High},
		{Tool: "copilot_cloud", Weak: []Signal{p("COPILOT_AGENT_JOB_ID"), p("COPILOT_AGENT")}, Source: "is-ai-agent; mise crates/mise-agent-env", Confidence: Low},
		{Tool: "aider", Procs: []ProcMatch{{Exe: "python", Script: "aider"}, {Exe: "python3", Script: "aider"}, {Exe: "aider"}},
			Source: "aider 0.81.3 aider/run_cmd.py (no marker)", Confidence: High},
		{Tool: "cline", Weak: []Signal{eq("CLINE_ACTIVE", "true")}, SessionEnv: []string{"CLINE_TASK_ID"},
			Source: "cline/cline apps/vscode/src/hosts/vscode/terminal/VscodeTerminalRegistry.ts", Confidence: Medium},
		{Tool: "roo_code", Weak: []Signal{p("ROO_CODE_TASK_ID")}, SessionEnv: []string{"ROO_CODE_TASK_ID"}, Source: "is-ai-agent; mise lib.rs", Confidence: Low},
		{Tool: "continue", Procs: []ProcMatch{{Exe: "node", Script: "cn"}, {Exe: "cn"}}, Source: "continuedev/continue extensions/cli/README.md", Confidence: Unknown},
		{Tool: "windsurf", Source: "none found (needs a live capture)", Confidence: Unknown},
		{Tool: "amp", Weak: []Signal{p("AMP_CURRENT_THREAD_ID")}, SessionEnv: []string{"AMP_CURRENT_THREAD_ID"},
			Procs: []ProcMatch{{Exe: "node", Script: "amp"}, {Exe: "amp"}}, Source: "is-ai-agent; aws-cli botocore/useragent.py", Confidence: Low},
		{Tool: "goose", Strong: []Signal{eq("GOOSE_TERMINAL", "1")}, AgentNames: []string{"goose"}, SessionEnv: []string{"AGENT_SESSION_ID"},
			ModelEnv: []string{"GOOSE_MODEL"}, Procs: []ProcMatch{{Exe: "goose"}}, Source: "aaif-goose/goose crates/goose/src/agents/retry.rs", Confidence: High},
		{Tool: "kiro", Strong: []Signal{{Env: "AGENT_CONTEXT_OUT", Match: BothWith, Value: "AGENT_DISPLAY_OUT"}}, Weak: []Signal{eq("TERM_PROGRAM", "kiro"), p("KIRO_AGENT_PATH")},
			Procs: []ProcMatch{{Exe: "kiro-cli"}}, Source: "kiro.dev/docs/cli/reference/built-in-tools", Confidence: Medium},
		{Tool: "zed", Weak: []Signal{eq("ZED_TERM", "true"), eq("TERM_PROGRAM", "zed")}, Source: "zed-industries/zed crates/terminal/src/terminal.rs", Confidence: Medium},
		{Tool: "hermes_agent", Strong: []Signal{eq("HERMES_AGENT", "true")}, AgentNames: []string{"hermes-agent"}, SessionEnv: []string{"HERMES_SESSION_ID"},
			Procs:  []ProcMatch{{Exe: "python", Script: "hermes"}, {Exe: "python3", Script: "hermes"}, {Exe: "hermes"}},
			Source: "NousResearch/hermes-agent tools/environments/base_session_env.py", Confidence: High},
		{Tool: "pi", Strong: []Signal{eq("PI_CODING_AGENT", "true")}, AgentNames: []string{"pi"}, SessionEnv: []string{"PI_SESSION_ID"},
			Procs: []ProcMatch{{Exe: "pi"}}, Source: "earendil-works/pi packages/coding-agent/src/cli/setup.ts", Confidence: High},
		{Tool: "grok_build", Weak: []Signal{p("GROK_SESSION_ID")}, SessionEnv: []string{"GROK_SESSION_ID"}, Procs: []ProcMatch{{Exe: "grok"}},
			Source: "is-ai-agent", Confidence: Low},
		{Tool: "grok_cli", Procs: []ProcMatch{{Exe: "node", Script: "grok"}}, Source: "superagent-ai/grok-cli (no marker)", Confidence: Unknown},
		{Tool: "crush", Strong: []Signal{eq("CRUSH", "1")}, AgentNames: []string{"crush"}, Procs: []ProcMatch{{Exe: "crush"}},
			Source: "charmbracelet/crush internal/shell/shell.go", Confidence: High},
		{Tool: "warp", Weak: []Signal{p("OZ_RUN_ID"), eq("TERM_PROGRAM", "WarpTerminal")}, SessionEnv: []string{"OZ_RUN_ID"}, Source: "Warp docs mirror (OZ_RUN_ID)", Confidence: Low},
		{Tool: "devin", Weak: []Signal{{Env: "EDITOR", Match: Contains, Value: "devin"}}, Source: "@vercel/detect-agent; is-ai-agent", Confidence: Low},
	}
}

// terminalEnv are the allowlisted terminal / IDE variables (section 6.3).
var terminalEnv = []string{"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERMINAL_EMULATOR"}

// AllowedNames is every variable name Detect may read a VALUE from (6.3:
// an allowlist in code, never an env dump). The allowlist test fails if
// any name looks token-like.
func AllowedNames(extra []Detector) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	add(EnvAIAgent)
	add(EnvAgent)
	for _, n := range terminalEnv {
		add(n)
	}
	for _, d := range append(Builtin(), extra...) {
		for _, s := range append(append([]Signal{}, d.Strong...), d.Weak...) {
			add(s.Env)
			if s.Match == BothWith {
				add(s.Value)
			}
		}
		for _, n := range d.SessionEnv {
			add(n)
		}
		for _, n := range d.ModelEnv {
			add(n)
		}
	}
	for _, c := range ciProviders() {
		add(c.Marker.Env)
		for _, n := range append(append([]string{}, c.RepoEnv...), c.RunEnv...) {
			add(n)
		}
		for _, n := range []string{c.AttemptEnv, c.WorkflowEnv, c.JobEnv, c.CommitEnv} {
			add(n)
		}
	}
	return out
}
