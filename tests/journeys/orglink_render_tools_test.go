//go:build journeys && !windows

package journeys

// orglink_render_tools_test.go — devco D-711: after `lore org link --own`,
// `lore render` wrote a pointer file for every tool of the person's setup
// (AGENTS.md, .cursor/rules/lore.mdc, .github/copilot-instructions.md)
// into a code repo that used only Claude Code. Now render writes only the
// tools the repo uses or the person picked for it, names each skipped one
// with the command that adds it, and that command works.

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyOrgLinkRenderOnlyUsedTools(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Castell", "mira@agency.example").Laptop()
	store := w.Remote("github.com/agency-co/lore-store")
	tools := jPlainRepo(t, mira, w.Remote("github.com/agency-co/tools"), filepath.Join(mira.Home, "work", "tools"))
	svc := jPlainRepo(t, mira, w.Remote("github.com/agency-co/svcapp"), filepath.Join(mira.Home, "work", "svcapp"))
	bare := jPlainRepo(t, mira, w.Remote("github.com/agency-co/bareapp"), filepath.Join(mira.Home, "work", "bareapp"))
	absent := func(c *teamharness.Clone, files ...string) {
		t.Helper()
		for _, f := range files {
			if _, ok := c.ReadFile(f); ok {
				t.Fatalf("D-711: %s written in %s, a repo that uses only Claude Code", f, filepath.Base(c.Dir))
			}
		}
	}

	w.Step("day1/setup-lists-five-tools-repo-has-claude-only", func() {
		mira.WriteHomeFile(".lore/config.yaml", "setups:\n  - name: agency\n    kind: solo\n    default: true\n    tools: [claude_code, codex, cursor, gemini_cli, copilot]\n    folders:\n      - "+filepath.Join(mira.Home, "work")+"\n")
		mira.LoreIn(tools, "store", "init", "--name", "agencystore", "--url", store.SSHURL()).OK(t)
		mira.LoreIn(tools, "org", "create", "--org", lgOrg, "--store", store.SSHURL(), "--own", lgOwn,
			"--key", "human:mira@agency.example", "--name", "Mira", "--yes").OK(t)
		for _, f := range []string{"svc", "bare"} {
			mira.LoreIn(tools, "store", "folder", "add", "agencystore", f).OK(t)
		}
		svc.WriteFile("CLAUDE.md", "# svc notes\n")
		mira.LoreIn(svc, "init", "--target", "claude_code").OK(t)
		mira.LoreIn(svc, "policy", "unset", "--key", "context.targets", "--yes").OK(t) // a repo from before the setting
		svc.CommitAll("lore")
		bare.WriteFile("CLAUDE.md", "# bare notes\n")
		bare.CommitAll("claude")
	})

	w.Step("day1/link-own-then-render-writes-claude-only", func() {
		mira.LoreIn(svc, "org", "link", "--org", lgOrg, "--project", "svc", "--own", "--yes").OK(t)
		mira.LoreIn(svc, "render", "--yes").OK(t).
			Says(t, "CLAUDE.md (pointer present)", "skipped codex (AGENTS.md)", "skipped cursor (.cursor/rules/lore.mdc)",
				"skipped gemini_cli (GEMINI.md)", "skipped copilot (.github/copilot-instructions.md)", "add it with: lore render --add-target cursor").
			Never(t, "AGENTS.md (pointer", "lore.mdc (pointer", "copilot-instructions.md (pointer", "GEMINI.md (pointer")
		absent(svc, "AGENTS.md", "GEMINI.md", ".cursor/rules/lore.mdc", ".cursor/rules/lore-private.mdc", ".github/copilot-instructions.md")
	})

	w.Step("day1/the-printed-command-adds-the-tool", func() {
		mira.LoreIn(svc, "render", "--add-target", "cursor", "--yes").OK(t).
			Says(t, "added cursor to context.targets", "lore.mdc (pointer").Never(t, "skipped")
		if _, ok := svc.ReadFile(".cursor/rules/lore.mdc"); !ok {
			t.Fatal("render --add-target cursor did not write .cursor/rules/lore.mdc")
		}
		absent(svc, "AGENTS.md", "GEMINI.md", ".github/copilot-instructions.md")
		mira.LoreIn(svc, "render", "--yes").OK(t).Says(t, "lore.mdc (pointer present)").Never(t, "skipped")
	})

	w.Step("day1/linked-repo-without-lore-folder-gets-no-cursor-file", func() {
		mira.LoreIn(bare, "org", "link", "--org", lgOrg, "--project", "bare", "--own", "--yes").OK(t)
		mira.LoreIn(bare, "render", "--yes").OK(t).
			Says(t, "wrote CLAUDE.local.md", "skipped cursor (.cursor/rules/lore-private.mdc)", "the tool's own file is here (.cursorrules or .cursor/rules/*.mdc)").
			Never(t, "lore-private.mdc (git-excluded)")
		absent(bare, ".cursor/rules/lore-private.mdc", "AGENTS.md")
	})
}
