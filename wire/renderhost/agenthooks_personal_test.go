package renderhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// claudeRepo is a git repo with a .claude folder; settings, when not "",
// is written as .claude/settings.json (untracked, as lore left it).
func claudeRepo(t *testing.T, settings string) string {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(toolprofile.ClaudeSharedSettings)), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// installPlans plans and writes --agent-hooks in root for the mode.
func installPlans(t *testing.T, root string, shared bool) []hookPlan {
	t.Helper()
	plans, err := planAgentHooks(context.Background(), root, false, false, shared)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]opssetup.HookPlan, 0, len(plans))
	for _, p := range plans {
		out = append(out, opssetup.HookPlan{Tool: p.tool, File: p.file, Note: p.note, Change: p.change, Hook: p.hook})
	}
	if err := writeHookPlans(context.Background(), root, out, false, shared); err != nil {
		t.Fatal(err)
	}
	return plans
}

// gitStatus is `git status --porcelain --untracked-files=all` in root.
func gitStatus(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=all").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestAgentHooksPersonalByDefault (devco D-732): lore's Claude Code hooks
// went into .claude/settings.json, an untracked file in every `git status`
// of a repo that does not commit one. Now they go in the personal
// settings.local.json (kept out of git through the clone's info/exclude),
// an entry an older lore left in settings.json moves there (the person's
// own keys in settings.json stay; a settings.json left empty is removed),
// and with context.hooks.agent_hooks shared they go in settings.json.
// Break-verified: with the old ConfigFile every assertion on the file
// location fails, and without dropFromOther the old entry stays.
func TestAgentHooksPersonalByDefault(t *testing.T) {
	t.Parallel()
	start, _ := toolprofile.SessionHookFor(toolprofile.NameClaudeCode)
	end, _ := toolprofile.SessionEndHookFor(toolprofile.NameClaudeCode)
	oldLore := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"` + start.Command() + `"}]}]}}`

	// a fresh repo: nothing lore writes shows in git status
	root := claudeRepo(t, "")
	installPlans(t, root, false)
	local := filepath.Join(root, filepath.FromSlash(toolprofile.ClaudeLocalSettings))
	for _, h := range []toolprofile.SessionHook{start, end} {
		if has, err := hookInstalled(local, h); err != nil || !has {
			t.Fatalf("fresh repo: %s hook not in settings.local.json (%v)", h.Event, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(toolprofile.ClaudeSharedSettings))); !os.IsNotExist(err) {
		t.Fatalf("fresh repo: lore wrote .claude/settings.json (%v)", err)
	}
	if st := gitStatus(t, root); st != "" {
		t.Fatalf("fresh repo: git status %q, want clean", st)
	}

	// an older lore's lore-only settings.json: moved, then removed
	root = claudeRepo(t, oldLore)
	plans := installPlans(t, root, false)
	moved := false
	for _, p := range plans {
		if p.hook.Event == start.Event && p.tool == toolprofile.NameClaudeCode && strings.Contains(p.note, "move SessionStart hook from .claude/settings.json") {
			moved = true
		}
	}
	if !moved {
		t.Fatalf("lore-only settings.json: the plan does not say it moves the hook: %+v", plans)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(toolprofile.ClaudeSharedSettings))); !os.IsNotExist(err) {
		t.Fatalf("lore-only settings.json: still there after the move (%v)", err)
	}
	if st := gitStatus(t, root); st != "" {
		t.Fatalf("lore-only settings.json: git status %q, want clean", st)
	}

	// the person's own keys in settings.json stay; only lore's entry leaves
	mixed := `{"permissions":{"allow":["Bash(go test:*)"]},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo hi"},{"type":"command","command":"` + start.Command() + `"}]}]}}`
	root = claudeRepo(t, mixed)
	installPlans(t, root, false)
	shared := filepath.Join(root, filepath.FromSlash(toolprofile.ClaudeSharedSettings))
	b, err := os.ReadFile(shared)
	if err != nil || !strings.Contains(string(b), "Bash(go test:*)") || !strings.Contains(string(b), "echo hi") || strings.Contains(string(b), "lore agent") {
		t.Fatalf("mixed settings.json: %q %v, want the person's entries kept and lore's gone", b, err)
	}
	if has, err := hookInstalled(filepath.Join(root, filepath.FromSlash(toolprofile.ClaudeLocalSettings)), start); err != nil || !has {
		t.Fatalf("mixed settings.json: the hook did not land in settings.local.json (%v)", err)
	}

	// the team opted in: settings.json, and the personal copy leaves
	installPlans(t, root, true)
	for _, h := range []toolprofile.SessionHook{start, end} {
		if has, err := hookInstalled(shared, h); err != nil || !has {
			t.Fatalf("shared: %s hook not in settings.json (%v)", h.Event, err)
		}
		if has, _ := hookInstalled(filepath.Join(root, filepath.FromSlash(toolprofile.ClaudeLocalSettings)), h); has {
			t.Fatalf("shared: %s hook still in settings.local.json", h.Event)
		}
	}
}
