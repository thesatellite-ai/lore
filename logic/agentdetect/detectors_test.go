package agentdetect

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

type fakeProcs []Process

func (f fakeProcs) Ancestors(max int) []Process {
	if len(f) > max {
		return f[:max]
	}
	return f
}

type fakeFiles map[string]string

func (f fakeFiles) ReadFile(n string) ([]byte, error) {
	if s, ok := f[n]; ok {
		return []byte(s), nil
	}
	return nil, errors.New("no file")
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// TestDetectPerTool pins one case per tool (OQ16): the exact variables the
// source shows give the tool and its session id.
func TestDetectPerTool(t *testing.T) {
	cases := []struct {
		name, tool, session string
		env                 map[string]string
		procs               fakeProcs
		tty                 bool
	}{
		{"claude_code", "claude_code", "s-cc", map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1", "CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-cc"}, nil, true},
		{"codex", "codex", "s-1", map[string]string{"CODEX_THREAD_ID": "t-1", "CODEX_SESSION_ID": "s-1"}, nil, true},
		{"opencode", "opencode", "o-1", map[string]string{"OPENCODE": "1", "AGENT": "1", "AI_AGENT": "opencode", "OPENCODE_SESSION_ID": "o-1"}, nil, true},
		{"gemini_cli", "gemini_cli", "", map[string]string{"GEMINI_CLI": "1"}, nil, true},
		{"qwen_code", "qwen_code", "", map[string]string{"QWEN_CODE": "1"}, nil, true},
		{"cursor", "cursor", "", map[string]string{"CURSOR_AGENT": "1"}, nil, true},
		{"copilot_cli", "copilot_cli", "c-1", map[string]string{"COPILOT_CLI": "1", "COPILOT_AGENT_SESSION_ID": "c-1"}, nil, true},
		{"copilot_vscode", "copilot_vscode", "", map[string]string{"AI_AGENT": "github_copilot_vscode_agent"}, nil, true},
		{"copilot_cloud", "copilot_cloud", "", map[string]string{"COPILOT_AGENT_JOB_ID": "j"}, nil, false},
		{"aider", "aider", "", map[string]string{}, fakeProcs{{Exe: "/bin/bash"}, {Exe: "/usr/bin/python3", Args: []string{"/home/u/.local/bin/aider"}}}, true},
		{"cline", "cline", "", map[string]string{"CLINE_ACTIVE": "true"}, nil, false},
		{"roo_code", "roo_code", "r-1", map[string]string{"ROO_CODE_TASK_ID": "r-1"}, nil, false},
		{"continue", "continue", "", map[string]string{}, fakeProcs{{Exe: "node", Args: []string{"/x/bin/cn"}}}, true},
		{"windsurf", "", "", map[string]string{}, nil, true},
		{"amp", "amp", "a-1", map[string]string{"AMP_CURRENT_THREAD_ID": "a-1"}, nil, false},
		{"goose", "goose", "", map[string]string{"GOOSE_TERMINAL": "1", "AGENT": "goose"}, nil, true},
		{"kiro", "kiro", "", map[string]string{"AGENT_CONTEXT_OUT": "/f1", "AGENT_DISPLAY_OUT": "/f2"}, nil, true},
		{"zed", "zed", "", map[string]string{"ZED_TERM": "true"}, nil, false},
		{"hermes_agent", "hermes_agent", "h-1", map[string]string{"HERMES_AGENT": "true", "AI_AGENT": "hermes-agent", "HERMES_SESSION_ID": "h-1"}, nil, true},
		{"pi", "pi", "", map[string]string{"PI_CODING_AGENT": "true", "AI_AGENT": "pi"}, nil, true},
		{"grok_build", "grok_build", "g-1", map[string]string{"GROK_SESSION_ID": "g-1"}, nil, false},
		{"grok_cli", "grok_cli", "", map[string]string{}, fakeProcs{{Exe: "node", Args: []string{"/x/grok.js"}}}, true},
		{"crush", "crush", "", map[string]string{"CRUSH": "1", "AGENT": "crush", "AI_AGENT": "crush"}, nil, true},
		{"warp", "warp", "oz-1", map[string]string{"OZ_RUN_ID": "oz-1"}, nil, false},
		{"devin", "devin", "", map[string]string{"EDITOR": "devin-edit"}, nil, false},
	}
	named := map[string]bool{}
	for _, c := range cases {
		named[c.name] = true
		t.Run(c.name, func(t *testing.T) {
			r, err := Detect(Inputs{Getenv: envOf(c.env), TTY: c.tty, Procs: c.procs})
			if err != nil {
				t.Fatal(err)
			}
			if r.Tool != c.tool || r.SessionID != c.session {
				t.Fatalf("got tool %q session %q, want %q %q (signal %q)", r.Tool, r.SessionID, c.tool, c.session, r.Signal)
			}
		})
	}
	// Completeness: every built-in row has a slug, a source, a confidence
	// and a test case naming it.
	for _, d := range Builtin() {
		if !slugRe.MatchString(d.Tool) || d.Source == "" || d.Confidence == "" || !named[d.Tool] {
			t.Errorf("detector %q lacks a slug, source, confidence or test case", d.Tool)
		}
	}
}

// TestDetectWeakOnly pins OQ11: weak alone with a TTY is no agent; without
// a TTY it is that tool with weak_only.
func TestDetectWeakOnly(t *testing.T) {
	for _, env := range []map[string]string{{"CURSOR_TRACE_ID": "x"}, {"ZED_TERM": "true"}, {"TERM_PROGRAM": "kiro"}, {"CLAUDECODE": "1"}} {
		r, _ := Detect(Inputs{Getenv: envOf(env), TTY: true})
		if r.IsAgent() {
			t.Fatalf("%v with a TTY gave %q", env, r.Tool)
		}
		r, _ = Detect(Inputs{Getenv: envOf(env), TTY: false})
		if !r.IsAgent() || !slices.Contains(r.Flags, FlagWeakOnly) {
			t.Fatalf("%v without a TTY gave %+v", env, r)
		}
	}
}

// TestDetectGeneric pins OQ15.
func TestDetectGeneric(t *testing.T) {
	for _, c := range []struct {
		env       map[string]string
		tool, ver string
	}{
		{map[string]string{"AI_AGENT": "1"}, UnknownAgent, ""},
		{map[string]string{"AGENT": "1"}, UnknownAgent, ""},
		{map[string]string{"AI_AGENT": "somethingnew"}, UnknownAgent, ""},
		{map[string]string{"AGENT": "build-01"}, "", ""},
		{map[string]string{"AI_AGENT": "claude-code_2.1.0_agent"}, "claude_code", "2.1.0"},
		{map[string]string{"AI_AGENT": "opencode@1.2.3"}, "opencode", "1.2.3"},
	} {
		r, _ := Detect(Inputs{Getenv: envOf(c.env), TTY: true})
		if r.Tool != c.tool || r.Version != c.ver {
			t.Fatalf("%v gave %q %q", c.env, r.Tool, r.Version)
		}
	}
}

// TestDetectForkAndNested: qwen over gemini; nearest ancestor wins.
func TestDetectForkAndNested(t *testing.T) {
	r, _ := Detect(Inputs{Getenv: envOf(map[string]string{"QWEN_CODE": "1", "GEMINI_CLI": "1"})})
	if r.Tool != "qwen_code" || !slices.Contains(r.Outer, "gemini_cli") {
		t.Fatalf("fork: %+v", r)
	}
	env := map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1", "CLAUDE_CODE_SESSION_ID": "outer", "CODEX_THREAD_ID": "t", "CODEX_SESSION_ID": "inner"}
	r, _ = Detect(Inputs{Getenv: envOf(env), Procs: fakeProcs{{Exe: "bash"}, {Exe: "codex"}, {Exe: "zsh"}, {Exe: "claude"}}})
	if r.Tool != "codex" || r.SessionID != "inner" || r.ParentSessionID != "outer" || !slices.Equal(r.Outer, []string{"claude_code"}) {
		t.Fatalf("nested: %+v", r)
	}
}

// TestDetectVersions pins OQ12: path, then metadata, never running the tool.
func TestDetectVersions(t *testing.T) {
	r, _ := Detect(Inputs{Getenv: envOf(map[string]string{"GOOSE_TERMINAL": "1"}), Procs: fakeProcs{{Exe: "/opt/homebrew/Cellar/goose/1.9.2/bin/goose"}}})
	if r.Version != "1.9.2" || r.Sources["tool_version"] != SourcePath {
		t.Fatalf("path: %+v", r)
	}
	files := fakeFiles{"/usr/lib/node_modules/@google/gemini-cli/package.json": `{"version":"0.58.0"}`}
	r, _ = Detect(Inputs{Getenv: envOf(map[string]string{"GEMINI_CLI": "1"}), Files: files,
		Procs: fakeProcs{{Exe: "node", Args: []string{"/usr/lib/node_modules/@google/gemini-cli/dist/gemini.js"}}}})
	if r.Version != "0.58.0" || r.Sources["tool_version"] != SourceMetadata {
		t.Fatalf("metadata: %+v", r)
	}
	r, _ = Detect(Inputs{Getenv: envOf(map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1", "AI_AGENT": "claude-code_2.1.0_agent"})})
	if r.Tool != "claude_code" || r.Version != "2.1.0" || r.Sources["tool_version"] != SourceEnv {
		t.Fatalf("env version: %+v", r)
	}
	r, _ = Detect(Inputs{Getenv: envOf(map[string]string{"GEMINI_CLI": "1"}), Files: fakeFiles{}})
	if r.Version != "" {
		t.Fatalf("blank: %+v", r)
	}
}

// TestDetectCIAndTrailer pins OQ13.
func TestDetectCIAndTrailer(t *testing.T) {
	env := map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": "acme/acmehub_backend", "GITHUB_RUN_ID": "123456789",
		"GITHUB_RUN_ATTEMPT": "1", "GITHUB_WORKFLOW": "ci", "GITHUB_JOB": "lore-check", "GITHUB_SHA": "4e6ed1c0ffee"}
	r, _ := Detect(Inputs{Getenv: envOf(env)})
	if r.IsAgent() || r.CI == nil || r.CI.ActorKey() != "ci:github_actions/acme/acmehub_backend" {
		t.Fatalf("ci: %+v", r)
	}
	want := "github_actions run 123456789 attempt 1, workflow ci, job lore-check, commit 4e6ed1c"
	if got := r.CI.Trailer(); got != want {
		t.Fatalf("trailer %q", got)
	}
}

// TestAllowlistAndPlugins: no token-like name is ever read (section 5); a
// planted token never reaches a result; plugins only add (PQ1).
func TestAllowlistAndPlugins(t *testing.T) {
	for _, n := range AllowedNames(nil) {
		if tokenLikeRe.MatchString(n) {
			t.Errorf("allowlisted name %q looks token-like", n)
		}
	}
	secret := "ghp_" + strings.Repeat("A", 36)
	env := map[string]string{"GITHUB_TOKEN": secret, "OPENAI_API_KEY": secret, "CI_JOB_TOKEN": secret, "CODEX_THREAD_ID": "t"}
	read := map[string]bool{}
	r, _ := Detect(Inputs{Getenv: func(k string) string { read[k] = true; return env[k] }})
	for k := range read {
		if !slices.Contains(AllowedNames(nil), k) {
			t.Errorf("read %q outside the allowlist", k)
		}
	}
	if strings.Contains(r.Signal+r.SessionID+r.Version+r.Terminal, secret) {
		t.Fatal("a token reached the result")
	}
	if _, err := Detect(Inputs{Getenv: envOf(nil), Extra: []Detector{{Tool: "codex"}}}); !errors.Is(err, ErrBuiltinSlug) {
		t.Fatalf("builtin slug: %v", err)
	}
	if _, err := Detect(Inputs{Getenv: envOf(nil), Extra: []Detector{{Tool: "inhouse", Strong: []Signal{p("MY_AUTH")}}}}); !errors.Is(err, ErrTokenName) {
		t.Fatalf("token name: %v", err)
	}
	extra := []Detector{{Tool: "inhouse", Strong: []Signal{eq("INHOUSE_AGENT", "1")}}}
	r, _ = Detect(Inputs{Getenv: envOf(map[string]string{"CODEX_THREAD_ID": "t", "INHOUSE_AGENT": "1"}), Extra: extra})
	if r.Tool != "codex" {
		t.Fatalf("a plugin took a built-in write: %+v", r)
	}
	r, _ = Detect(Inputs{Getenv: envOf(map[string]string{"INHOUSE_AGENT": "1"}), Extra: extra})
	if r.Tool != "inhouse" || r.Signal != "plugin:inhouse/INHOUSE_AGENT" {
		t.Fatalf("plugin: %+v", r)
	}
}
