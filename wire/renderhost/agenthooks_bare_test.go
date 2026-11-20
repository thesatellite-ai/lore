package renderhost

// agenthooks_bare_test.go — devco D-743: moving or removing lore's agent
// hooks never leaves a bare {} settings file, and never rewrites a
// committed settings file that holds the team's own settings.

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

// commitAll commits every file of root (no hooks run: a plain fixture).
func commitAll(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=Dana Dev", "-c", "user.email=dana@example.com", "commit", "-q", "--no-verify", "-m", "fixture"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

// changedPlans plans --agent-hooks (or --remove) in root and returns the
// plans that would rewrite a file.
func changedPlans(t *testing.T, root string, remove bool) []opssetup.HookPlan {
	t.Helper()
	plans, err := planAgentHooks(context.Background(), root, remove, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var out []opssetup.HookPlan
	for _, p := range plans {
		if p.change {
			out = append(out, opssetup.HookPlan{Tool: p.tool, File: p.file, Note: p.note, Change: p.change, Hook: p.hook})
		}
	}
	return out
}

// TestAgentHooksNoBareSettingsFile (devco D-743): lore doctor --fix moved
// lore's hooks out of a committed .claude/settings.json that held nothing
// else and left a bare {} file there; with the team's own keys beside them
// it rewrote (and reformatted) that committed file. Now a file lore's
// hooks were all of is removed, tracked or not; a tracked one with other
// keys stays byte for byte (and is not planned again on the next run);
// --remove leaves no {} file either. Break-verified: with the old
// removeIfEmptyUntracked the tracked lore-only file stays as {}, without
// otherKept the mixed file is rewritten, and without the remove sweep
// settings.local.json stays as {}.
func TestAgentHooksNoBareSettingsFile(t *testing.T) {
	t.Parallel()
	start, _ := toolprofile.SessionHookFor(toolprofile.NameClaudeCode)
	end, _ := toolprofile.SessionEndHookFor(toolprofile.NameClaudeCode)
	entry := func(cmd string) string {
		return `[{"hooks":[{"type":"command","command":"` + cmd + `"}]}]`
	}
	sharedRel := filepath.FromSlash(toolprofile.ClaudeSharedSettings)
	localRel := filepath.FromSlash(toolprofile.ClaudeLocalSettings)

	// a committed settings.json holding only lore's hooks: removed
	root := claudeRepo(t, `{"hooks":{"SessionStart":`+entry(start.Command())+`,"SessionEnd":`+entry(end.Command())+`}}`)
	commitAll(t, root)
	installPlans(t, root, false)
	if b, err := os.ReadFile(filepath.Join(root, sharedRel)); !os.IsNotExist(err) {
		t.Fatalf("lore-only committed settings.json: still there after the move: %q (%v)", b, err)
	}
	if st := gitStatus(t, root); st != "D "+toolprofile.ClaudeSharedSettings && st != " D "+toolprofile.ClaudeSharedSettings {
		t.Fatalf("lore-only committed settings.json: git status %q, want only its deletion", st)
	}
	if has, err := hookInstalled(filepath.Join(root, localRel), start); err != nil || !has {
		t.Fatalf("lore-only committed settings.json: the hook did not land in settings.local.json (%v)", err)
	}

	// a committed settings.json with the team's own keys: untouched
	mixed := "{\n  \"permissions\": {\"allow\": [\"Bash(make test)\"]},\n  \"hooks\": {\"SessionStart\": " + entry(start.Command()) + "}\n}\n"
	root = claudeRepo(t, mixed)
	commitAll(t, root)
	installPlans(t, root, false)
	if b, err := os.ReadFile(filepath.Join(root, sharedRel)); err != nil || string(b) != mixed {
		t.Fatalf("mixed committed settings.json: rewritten to %q (%v), want it untouched", b, err)
	}
	if has, err := hookInstalled(filepath.Join(root, localRel), start); err != nil || !has {
		t.Fatalf("mixed committed settings.json: the hook did not land in settings.local.json (%v)", err)
	}
	if again := changedPlans(t, root, false); len(again) != 0 {
		t.Fatalf("mixed committed settings.json: the next run plans changes again: %+v", again)
	}
	plans, err := planAgentHooks(context.Background(), root, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	said := false
	for _, p := range plans {
		if p.hook.Event == start.Event && p.hook.Cmd == "" && strings.Contains(p.note, "stays: git tracks that file") {
			said = true
		}
	}
	if !said {
		t.Fatalf("mixed committed settings.json: the plan does not say lore's entry there stays: %+v", plans)
	}

	// --remove: lore's last entry out of settings.local.json leaves no {}
	root = claudeRepo(t, "")
	installPlans(t, root, false)
	if err := writeHookPlans(context.Background(), root, changedPlans(t, root, true), true, false); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, localRel)); !os.IsNotExist(err) {
		t.Fatalf("--remove: settings.local.json left as %q (%v), want it removed", b, err)
	}
}

// TestOnlyLoreHooks: the shape test behind otherKept.
func TestOnlyLoreHooks(t *testing.T) {
	t.Parallel()
	start, _ := toolprofile.SessionHookFor(toolprofile.NameClaudeCode)
	ctx := toolprofile.ContextHooksFor(toolprofile.NameClaudeCode, false)[0]
	for _, c := range []struct {
		name, doc string
		want      bool
	}{
		{"empty", `{}`, true},
		{"lore session hook", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"` + start.Command() + `"}]}]}}`, true},
		{"lore context hook with its matcher", `{"hooks":{"PreToolUse":[{"matcher":"Read|Edit|Write","hooks":[{"type":"command","command":"` + ctx.Command() + `"}]}]}}`, true},
		{"another key", `{"permissions":{},"hooks":{}}`, false},
		{"their own hook", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`, false},
	} {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(c.doc), 0o600); err != nil {
			t.Fatal(err)
		}
		doc, err := readSettings(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := onlyLoreHooks(doc); got != c.want {
			t.Errorf("%s: onlyLoreHooks = %v, want %v", c.name, got, c.want)
		}
	}
}
