package renderhost

// sessionend_hook_test.go — the session-end hook is planned, installed,
// removed and checked (tools.hooks reads the same plan) like the
// session-start hook (decided 2026-11-10, land7 A).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// endHookEntries is every hooks.SessionEnd entry of the settings file at path.
func endHookEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var out []map[string]any
	for _, g := range doc.Hooks[toolprofile.EventSessionEnd] {
		out = append(out, g.Hooks...)
	}
	return out
}

// TestSessionEndHookPlannedInstalledRemoved: every tool with a session
// hook also plans `lore agent end --tool <slug>` under SessionEnd; writing
// the plan installs it (Claude Code's with a timeout above lore's own
// bound, since its SessionEnd hooks otherwise share 1.5 s), a second plan
// says "already installed", a plan with only the start hook installed is
// a change (so doctor's tools.hooks warns), and --remove takes it out
// again, keeping the user's own SessionEnd hook. Break-verified by
// dropping the end hook from planAgentHooks (every assertion fails) and by
// not writing the timeout.
func TestSessionEndHookPlannedInstalledRemoved(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, d := range []string{".claude", ".codex", ".gemini"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	claudeFile := filepath.Join(root, ".claude", "settings.json")
	own := `{"hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"echo bye"}]}]}}`
	if err := os.WriteFile(claudeFile, []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	plans, err := planAgentHooks(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	ends := map[string]hookPlan{}
	for _, p := range plans {
		if p.hook.Event == toolprofile.EventSessionEnd {
			ends[p.tool] = p
		}
	}
	for _, tool := range []string{toolprofile.NameClaudeCode, toolprofile.NameCodex, toolprofile.NameGeminiCLI} {
		p, ok := ends[tool]
		if !ok || !p.change || p.hook.Command() != "lore agent end --tool "+tool {
			t.Fatalf("%s: want a SessionEnd plan adding `lore agent end --tool %s`, got %+v", tool, tool, p)
		}
	}
	for _, p := range plans {
		if p.change {
			if err := writeHookFile(root, filepath.Join(root, filepath.FromSlash(p.hook.ConfigFile)), p.hook, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	got := endHookEntries(t, claudeFile)
	if len(got) != 2 || got[0]["command"] != "echo bye" {
		t.Fatalf("Claude Code SessionEnd: want the user's hook kept and lore's added, got %v", got)
	}
	if got[1]["command"] != toolprofile.EndHookCommand(toolprofile.NameClaudeCode) || got[1]["timeout"] != float64(toolprofile.EndHookTimeout) {
		t.Fatalf("Claude Code SessionEnd: want lore's hook with timeout %d, got %v", toolprofile.EndHookTimeout, got[1])
	}
	for _, f := range []string{".codex/hooks.json", ".gemini/settings.json"} {
		e := endHookEntries(t, filepath.Join(root, filepath.FromSlash(f)))
		if len(e) != 1 || !strings.HasPrefix(e[0]["command"].(string), "lore agent end --tool ") {
			t.Fatalf("%s SessionEnd: got %v", f, e)
		}
		if _, has := e[0]["timeout"]; has {
			t.Fatalf("%s: only Claude Code's end hook needs a timeout, got %v", f, e[0])
		}
	}
	again, err := planAgentHooks(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range again {
		if p.change {
			t.Fatalf("after installing, the plan still changes %s %s: %s", p.tool, p.hook.Event, p.note)
		}
	}
	// A repo that has only the start hook (installed before the end hook
	// existed) is not fully set up: the plan adds the end hook, so the
	// tools.hooks check reports it missing.
	end, _ := toolprofile.SessionEndHookFor(toolprofile.NameClaudeCode)
	if err := writeHookFile(root, claudeFile, end, true); err != nil {
		t.Fatal(err)
	}
	if got := endHookEntries(t, claudeFile); len(got) != 1 || got[0]["command"] != "echo bye" {
		t.Fatalf("remove: want only the user's SessionEnd hook left, got %v", got)
	}
	p, err := planOneHook(root, toolprofile.NameClaudeCode, end, false)
	if err != nil || !p.change || !strings.Contains(p.note, "add SessionEnd hook") {
		t.Fatalf("start hook only: want the end hook planned as missing, got %+v %v", p, err)
	}
	// --remove plans the end hook's removal with the others.
	rm, err := planAgentHooks(root, true, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rm {
		if p.hook.Event == toolprofile.EventSessionEnd && p.tool != toolprofile.NameClaudeCode && !p.change {
			t.Fatalf("--remove does not remove %s's end hook: %+v", p.tool, p)
		}
	}
}
