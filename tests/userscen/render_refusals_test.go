//go:build userscen

package userscen

// render_refusals_test.go — render and the global rule refusing a wrong
// input before anything is written (integration leftovers INTEG-RENDER-1..3):
//   - a typo'd profile name after --target (claud_code) is refused naming
//     the known profiles, and creates no file; a file of that name is still
//     wired when written as a path;
//   - --target in a repo whose context.targets row names a profile this
//     machine lacks refuses the whole render with nothing written, never
//     LORE.md and the pointer files and then an error;
//   - `rule add --scope global` (and --to me) with no personal store
//     attached says what is missing and how to make it.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestRenderTargetTypoRefused(t *testing.T) {
	c := renderFreshNoAgents(t)
	c.MustLore("rule", "add", "--body", "the typo rule", "--severity", "must")
	before := renderSnapshot(c, renderLoreMD, renderFullMD, "AGENTS.md")
	for _, args := range [][]string{
		{"render", "--target", "claud_code"},
		{"render", "--target", "codex,claud_code"},
		{"render", "--dry-run", "--target", "claud_code"},
		{"directive", "install", "--target", "claud_code"},
	} {
		c.Lore(args...).Fails(t, 20, "[E_INVALID_INPUT]", `--target "claud_code": unknown tool profile`, "claude_code", "codex", "--target ./claud_code")
		if _, ok := c.ReadFile("claud_code"); ok {
			t.Fatalf("%v: a typo'd profile name creates no file", args)
		}
		renderSameFiles(t, strings.Join(args, " "), before, renderSnapshot(c, renderLoreMD, renderFullMD, "AGENTS.md"))
	}
	// A file whose name looks like a profile is wired when named as a path.
	c.MustLore("render", "--target", "./notes_for_agents")
	if got := renderRead(t, c, "notes_for_agents"); got != renderPtrAt {
		t.Errorf("a path target gets the @import pointer: %q", got)
	}
	// Any other file name (a dot, a slash, capitals) still works as before.
	c.MustLore("render", "--target", "AI_NOTES.md")
	if got := renderRead(t, c, "AI_NOTES.md"); got != renderPtrAt {
		t.Errorf("a file target gets the @import pointer: %q", got)
	}
}

func TestRenderTargetWithTeammateProfileInRowRefusesWhole(t *testing.T) {
	c := renderFreshNoAgents(t)
	c.A.WriteHomeFile(".lore/config.yaml", renderProfileConfig("    - name: mytool\n      pointer_file: docs/MYTOOL.md\n      import: read\n"))
	c.MustLore("rule", "add", "--body", "the row rule", "--severity", "must")
	c.MustLore("policy", "set", "context.targets", "mytool", "--yes")
	c.MustLore("render")
	// The row's profile is a teammate's: this machine's config lacks it.
	c.A.WriteHomeFile(".lore/config.yaml", renderProfileConfig("    []\n"))
	files := []string{renderLoreMD, renderFullMD, "GEMINI.md", "docs/MYTOOL.md"}
	c.MustLore("rule", "add", "--body", "a second rule after the row", "--severity", "must")
	before := renderSnapshot(c, files...)
	c.Lore("render", "--target", "gemini_cli").Fails(t, 20, "[E_INVALID_INPUT]",
		`the repo's context.targets setting: unknown tool profile "mytool"`, "lore policy set context.targets <profiles> --yes")
	renderSameFiles(t, "render --target with an unknown profile in the row", before, renderSnapshot(c, files...))
	if _, ok := c.ReadFile("GEMINI.md"); ok {
		t.Error("the refused render wrote no GEMINI.md")
	}
	// Fixing the row lets the same command through.
	c.MustLore("policy", "set", "context.targets", "gemini_cli", "--yes")
	c.MustLore("render", "--target", "gemini_cli")
	if got := renderRead(t, c, "GEMINI.md"); got != renderPtrAt {
		t.Errorf("after the fix GEMINI.md carries the pointer: %q", got)
	}
}

// TestRenderGlobalRuleNeedsPersonalStore: a person (or their agent) with
// no personal store attached who writes a global rule is told what is
// missing and how to make it, not "write the actor row to store me"
// (INTEG-RENDER-3); after `lore store init me` the same command works.
// With v0.1.17's personal.store `local` the global rule keeps working.
func TestRenderGlobalRuleNeedsPersonalStore(t *testing.T) {
	t.Run("no personal store", func(t *testing.T) {
		cfg := renderSoloConfig()
		cfg.Personal = nil
		c := renderRepoFor(t, renderSlugOwn, cfg)
		agent := c.As(c.A.Agent("fe-1"))
		for _, cl := range []*th.Clone{c, agent} {
			for _, args := range [][]string{
				{"rule", "add", "--body", "Answer in short sentences", "--scope", "global"},
				{"memory", "add", "--body", "I prefer tabs", "--to", "me"},
			} {
				// --to me already named the missing store; --scope global
				// said "write the actor row to store me" with no hint.
				cl.Lore(args...).Fails(t, 27, "[E_NOT_FOUND]", "your personal store is not set up: run `lore store init me`").
					Never(t, "actor row")
			}
		}
		c.Lore("rule", "add", "--body", "Answer in short sentences", "--scope", "global").Says(t, "no personal store is attached on this machine")
		c.MustLore("rule", "list", "--json").Never(t, "Answer in short sentences")
		c.MustLore("store", "init", "me", "--yes")
		c.MustLore("rule", "add", "--body", "Answer in short sentences", "--scope", "global").Says(t, "[me]", "scope: [global]")
		agent.MustLore("rule", "add", "--body", "Answer briefly", "--scope", "global").Says(t, "[me]")
	})
	t.Run("personal.store local", func(t *testing.T) {
		c := renderRepoFor(t, renderSlugOwn, renderSoloConfig())
		c.MustLore("rule", "add", "--body", "Answer in short sentences", "--scope", "global").Says(t, "[me/everywhere]", "scope: [global]")
		c.As(c.A.Agent("fe-1")).MustLore("rule", "add", "--body", "Answer briefly", "--scope", "global").Says(t, "[me/everywhere]")
	})
}
