package app

// Two release-binary paths run through main in a child process
// (cliworld_test.go; later.md 396, 435):
// a row write from a subfolder of the repo, and the claude_code session
// hook that `lore setup --agent-hooks` installs.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeEnv are the variables agent detection reads for claude_code
// (logic/agentdetect detectors.go); the tests set or clear all of them so
// the machine the test runs on cannot change who writes.
var claudeEnv = []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_REMOTE_SESSION_ID"}

// asPerson clears every claude marker and sets LORE_HUMAN=1, so the write
// is the person's even when an ancestor process is a coding tool.
func asPerson(t *testing.T) {
	t.Helper()
	for _, k := range claudeEnv {
		t.Setenv(k, "")
	}
	t.Setenv("LORE_HUMAN", "1")
}

// asClaude makes this process a claude_code session with the given tool
// session id.
func asClaude(t *testing.T, session string) {
	t.Helper()
	for _, k := range claudeEnv {
		t.Setenv(k, "")
	}
	t.Setenv("LORE_HUMAN", "")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", session)
}

// TestMemoryAddFromSubfolder (later.md 396, v0.1.17 owner flag): a
// memory add run from a subfolder at any depth, by a person or an agent,
// exits 0 with no E_INTERNAL ("stored but its audit record failed" made
// agents retry and write duplicates), writes exactly one row file in the
// repo's own .lore (never a nested .lore in the subfolder), and the row
// is listed from that subfolder.
func TestMemoryAddFromSubfolder(t *testing.T) {
	t.Parallel()
	callers := []struct {
		name string
		set  func(w *cliWorld) *cliWorld
	}{
		{"person", (*cliWorld).person},
		{"claude_code agent", func(w *cliWorld) *cliWorld { return w.claude("sub-folder-session-0001") }},
	}
	for _, c := range callers {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := c.set(newCLIWorld(t))
			root := w.initRepo(t)
			for i, rel := range []string{".", "src", filepath.Join("src", "pkg", "deep")} {
				dir := filepath.Join(root, rel)
				if err := os.MkdirAll(dir, 0o750); err != nil {
					t.Fatal(err)
				}
				body := "subfolder note " + rel
				code, out, errOut := w.lore(t, dir, "memory", "add", "--body", body)
				if code != 0 || strings.Contains(errOut, "E_INTERNAL") || strings.Contains(errOut, "ERROR") {
					t.Fatalf("%s: memory add exit %d\nstdout: %s\nstderr: %s", rel, code, out, errOut)
				}
				files, err := filepath.Glob(filepath.Join(root, ".lore", "data", "memories", "*.json"))
				if err != nil || len(files) != i+1 {
					t.Fatalf("%s: %d row files after %d adds (%v): a failed or doubled write", rel, len(files), i+1, err)
				}
				if rel != "." {
					if _, err := os.Stat(filepath.Join(dir, ".lore")); !os.IsNotExist(err) {
						t.Fatalf("%s: a nested .lore was made in the subfolder (%v)", rel, err)
					}
				}
				code, out, errOut = w.lore(t, dir, "memory", "list")
				if code != 0 || !strings.Contains(out, body) {
					t.Fatalf("%s: memory list exit %d, row %q not listed\nstdout: %s\nstderr: %s", rel, code, body, out, errOut)
				}
			}
		})
	}
}

// hookSession is one row of `lore session list --json` (the fields this
// test reads).
type hookSession struct {
	Tool    string            `json:"tool"`
	Model   string            `json:"model"`
	Sources map[string]string `json:"sources"`
}

// TestClaudeSessionHookEndToEnd (later.md 435, OV11): `lore setup
// --agent-hooks --yes` writes the claude_code SessionStart hook; the hook
// command fed Claude Code's SessionStart JSON records the model on this
// session's row (source session_hook), `lore whoami` then shows it, and a
// broken input (not JSON, a list, cut short, empty) never fails the tool's session (exit 0, one
// stderr line) and never sets a model.
func TestClaudeSessionHookEndToEnd(t *testing.T) {
	t.Parallel()
	const session = "hook-e2e-session-0001"
	w := newCLIWorld(t).claude(session)
	root := w.initRepo(t)
	if err := os.Mkdir(filepath.Join(root, ".claude"), 0o750); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := w.lore(t, root, "setup", "--agent-hooks", "--yes"); code != 0 {
		t.Fatalf("setup --agent-hooks: exit %d\n%s\n%s", code, out, errOut)
	}
	settings, err := os.ReadFile(filepath.Clean(filepath.Join(root, ".claude", "settings.json")))
	if err != nil {
		t.Fatal(err)
	}
	const hookCmd = "lore agent start --tool claude_code --hook-stdin"
	if !strings.Contains(string(settings), hookCmd) || !strings.Contains(string(settings), "SessionStart") {
		t.Fatalf("settings.json has no SessionStart hook %q:\n%s", hookCmd, settings)
	}
	hookArgs := strings.Fields(hookCmd)[1:]

	// Broken inputs first: exit 0, no model recorded.
	for _, bad := range []string{"not json", `["a list"]`, `{"session_id":`, ""} {
		code, _, errOut := w.loreIn(t, root, nil, bad, hookArgs...)
		if code != 0 || strings.Count(strings.TrimSpace(errOut), "\n") > 0 {
			t.Fatalf("hook input %q: exit %d, stderr %q (want exit 0 and at most one line)", bad, code, errOut)
		}
	}
	if m := sessionModels(t, w, root); len(m) != 0 {
		t.Fatalf("a broken hook input recorded a model: %v", m)
	}

	in := `{"session_id":"` + session + `","model":"claude-e2e-model","hook_event_name":"SessionStart","source":"startup"}`
	code, out, errOut := w.loreIn(t, root, nil, in, hookArgs...)
	if code != 0 || errOut != "" {
		t.Fatalf("hook: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if m := sessionModels(t, w, root); len(m) != 1 || m[0] != "claude-e2e-model" {
		t.Fatalf("session rows with a hook model = %v, want [claude-e2e-model]", m)
	}
	code, out, errOut = w.lore(t, root, "whoami")
	if code != 0 || !strings.Contains(out, "claude-e2e-model") || !strings.Contains(out, "session hook") {
		t.Fatalf("whoami does not show the hook model: exit %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, session) {
		t.Fatalf("whoami printed the raw tool session id:\n%s", out)
	}
}

// sessionModels lists the models the session hook recorded on this
// machine's claude_code session rows.
func sessionModels(t *testing.T, w *cliWorld, dir string) []string {
	t.Helper()
	code, out, errOut := w.lore(t, dir, "session", "list", "--json")
	if code != 0 {
		t.Fatalf("session list: exit %d %s", code, errOut)
	}
	var got struct {
		Sessions []hookSession `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("session list --json: %v\n%s", err, out)
	}
	var models []string
	for _, s := range got.Sessions {
		if s.Tool == "claude_code" && s.Model != "" && s.Sources["model"] == "session_hook" {
			models = append(models, s.Model)
		}
	}
	return models
}
