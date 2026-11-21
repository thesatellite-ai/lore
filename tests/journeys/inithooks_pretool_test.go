//go:build journeys && !windows

package journeys

// inithooks_pretool_test.go — devco D-835: a repo wired for a coding
// tool with lore init --target gets lore's pre-edit context hook.
//
// lore init --target claude_code in a repo with no .claude/ folder wired
// CLAUDE.md and said nothing about hooks, and lore setup --agent-hooks
// then skipped the tool ("not set up here (.claude/ missing)"), so the
// agent edited files without ever seeing their hotfixes (the blind trap
// run: the lore agent fell into 4 of 7 hotfix traps). Now init names the
// missing hooks and the command, that command installs them for the tool
// the repo targets, and the PreToolUse hook puts the file's hotfix in
// front of the agent (and nothing for a file no row applies to).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyInitTargetGetsPreEditHook(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	noor := w.Persona("Noor Vale", "noor@orbit.example").Laptop()
	orbit := jPlainRepo(t, noor, w.Remote("github.com/orbit-co/orbit"), filepath.Join(noor.Home, "work", "orbit"))
	const hotfix = "billing/invoice.go dated invoices in local time. Use clock.UTC() there, never time.Now()."
	const hooksCmd = "lore setup --agent-hooks --yes"

	w.Step("day1/init-names-the-hooks", func() {
		r := noor.LoreIn(orbit, "init", "--name", "orbit", "--target", "claude_code").OK(t)
		out := r.Stdout + r.Stderr
		if !strings.Contains(out, "pre-edit context hooks of claude_code are not installed") || !strings.Contains(out, "install them with: "+hooksCmd) {
			t.Fatalf("lore init --target claude_code does not name the missing hooks:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(orbit.Dir, ".claude")); !os.IsNotExist(err) {
			t.Fatalf("init itself made .claude/ (%v): the hooks are the person's choice", err)
		}
		noor.LoreIn(orbit, "hotfix", "add", "--title", "Invoices dated a day off", "--severity", "critical", "--body", hotfix).OK(t)
	})

	w.Step("day1/the-printed-command-installs-the-pre-edit-hook", func() {
		noor.LoreIn(orbit, strings.Fields(hooksCmd)[1:]...).OK(t)
		b, err := os.ReadFile(filepath.Join(orbit.Dir, ".claude", "settings.local.json"))
		if err != nil {
			t.Fatalf("no .claude/settings.local.json after %s: %v", hooksCmd, err)
		}
		var doc struct {
			Hooks map[string][]struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("settings.local.json: %v\n%s", err, b)
		}
		pre := false
		for _, g := range doc.Hooks["PreToolUse"] {
			for _, h := range g.Hooks {
				if h.Command == "lore context --hook claude_code --hook-stdin" && strings.Contains(g.Matcher, "Edit") {
					pre = true
				}
			}
		}
		if !pre {
			t.Fatalf("no PreToolUse context hook on Edit:\n%s", b)
		}
		if again := noor.LoreIn(orbit, "init", "--target", "claude_code"); strings.Contains(again.Stdout+again.Stderr, "are not installed") {
			t.Fatalf("a second init still says the hooks are missing:\n%s", again.Stdout+again.Stderr)
		}
	})

	w.Step("day1/the-hook-shows-the-hotfix-before-the-edit", func() {
		in := func(file string) string {
			return `{"session_id":"s-1","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"` + filepath.Join(orbit.Dir, file) + `"}}`
		}
		r := noor.LoreStdinIn(orbit, in("billing/invoice.go"), "context", "--hook", "claude_code", "--hook-stdin").OK(t)
		if !strings.Contains(r.Stdout, "additionalContext") || !strings.Contains(r.Stdout, "clock.UTC()") {
			t.Fatalf("the pre-edit hook does not show the file's hotfix:\n%s", r.Stdout)
		}
		quiet := noor.LoreStdinIn(orbit, in("main.go"), "context", "--hook", "claude_code", "--hook-stdin").OK(t)
		if strings.TrimSpace(quiet.Stdout) != "" {
			t.Fatalf("the pre-edit hook spoke for a file no row applies to:\n%s", quiet.Stdout)
		}
	})
}
