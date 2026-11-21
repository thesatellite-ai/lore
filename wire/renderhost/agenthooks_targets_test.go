package renderhost

// agenthooks_targets_test.go — devco D-835: a repo wired for a tool by
// lore init --target (context.targets) gets that tool's hooks from lore
// setup --agent-hooks even before the tool's config folder exists.

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

// TestAgentHooksForTargetedToolWithoutFolder (devco D-835): lore init
// --target claude_code in a repo with no .claude/ folder wired CLAUDE.md,
// and lore setup --agent-hooks then said "not set up here (.claude/
// missing)", so the agent never got the pre-edit context hook that puts a
// file's hotfixes in front of it. Now a tool context.targets names is
// planned as an add (the folder is created on write); a tool it does not
// name keeps "not set up here", and --remove never creates a folder.
// Break-verified: with the folder check alone (targeted ignored) the
// claude_code hooks stay "not set up here".
func TestAgentHooksForTargetedToolWithoutFolder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	ctx := context.Background()
	plans, err := planAgentHooks(ctx, root, false, false, false, toolprofile.NameClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	var adds []opssetup.HookPlan
	for _, p := range plans {
		switch {
		case p.tool == toolprofile.NameClaudeCode:
			if !p.change || !strings.HasPrefix(p.note, "add ") {
				t.Fatalf("claude_code is in context.targets but its hook is not planned as an add: %+v", p)
			}
			adds = append(adds, opssetup.HookPlan{Tool: p.tool, File: p.file, Note: p.note, Change: p.change, Hook: p.hook})
		case p.tool == toolprofile.NameCodex && p.change:
			t.Fatalf("codex is not in context.targets and has no .codex/, but is planned: %+v", p)
		}
	}
	pre := false
	for _, a := range adds {
		if a.Hook.Event == "PreToolUse" && strings.Contains(a.Hook.Command(), "lore context --hook "+toolprofile.NameClaudeCode) {
			pre = true
		}
	}
	if !pre {
		t.Fatalf("no pre-edit context hook among the planned adds: %+v", adds)
	}
	if err := writeHookPlans(ctx, root, adds, false, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".claude", "settings.local.json"))
	if err != nil || !strings.Contains(string(b), "lore context --hook "+toolprofile.NameClaudeCode+" --hook-stdin") {
		t.Fatalf("settings.local.json after the write: %q (%v)", b, err)
	}

	// no target, no folder: unchanged behaviour
	bare := t.TempDir()
	if out, err := exec.Command("git", "-C", bare, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	for _, remove := range []bool{false, true} {
		targets := []string{}
		if remove {
			targets = []string{toolprofile.NameClaudeCode}
		}
		plans, err = planAgentHooks(ctx, bare, remove, false, false, targets...)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range plans {
			if p.change {
				t.Fatalf("remove=%v targets=%v: a repo with no tool folder plans a change: %+v", remove, targets, p)
			}
		}
	}
}
