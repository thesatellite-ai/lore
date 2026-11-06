//go:build teame2e

package teame2e

// later.md 617 on cmd/lore: `lore setup` is re-runnable and leaves search
// working (the v0.1.17 lore.db registry fingerprint stamp is retired with
// the ent cache, TE-D-1), and a teammate's render names a pointer file the
// team's context.targets no longer lists instead of rewriting it.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teDUnlistedLine is render's note for a wired CLAUDE.md that
// context.targets does not name (v0.1.17 text, oldcli_p2b TestRender_UnlistedPointerFileIsNamed).
const teDUnlistedLine = "CLAUDE.md carries lore's pointer but context.targets does not name it, so render leaves it as is; list it with: lore policy set context.targets <profiles incl. claude_code> --yes"

func TestTeD_SetupRerunAndUnlistedPointerAcrossClones(t *testing.T) {
	w := newTeamWorld(t, th.Cmd("setup"), th.Cmd("render"), th.Cmd("policy", "set"))
	r := w.Remote(slugLoose)
	bob := w.Human("bob", "bob@solo.example")
	bob.WriteConfig(teDSoloConfig("human:bob@solo.example", "Bob Roe", "bob"))
	carol := w.Human("carol", "carol@solo.example")
	carol.WriteConfig(teDSoloConfig("human:carol@solo.example", "Carol Poe", "carol"))

	bc := bob.Clone(r, "tool")
	bc.MustLore("init", "--non-interactive", "--name=tool")
	bc.MustLore("render", "--target", "claude_code")
	bc.MustLore("memory", "add", "--body", "searchable after setup")
	bc.CommitAll("lore")
	bc.AdminPush("origin", "main")

	// carol's fresh clone: setup builds her search index from the row
	// files bob pushed; running it again is fine.
	cc := carol.Clone(r, "tool")
	cc.MustLore("setup").Says(t, "setup complete")
	cc.MustLore("setup").Says(t, "setup complete")
	cc.MustLore("search", "searchable").Says(t, "Found 1 hit for", "searchable after setup")

	before, ok := cc.ReadFile("CLAUDE.md")
	if !ok || !strings.Contains(before, ".lore/LORE.md") {
		t.Fatalf("bob's committed CLAUDE.md carries the pointer: %q", before)
	}
	// bob narrows the team's targets to codex and pushes the policy row.
	bc.MustLore("policy", "set", "context.targets", "codex", "--yes")
	bc.CommitAll("targets")
	bc.AdminPush("origin", "main")

	cc.Git("pull", "-q")
	cc.MustLore("render").Says(t, teDUnlistedLine)
	if after, _ := cc.ReadFile("CLAUDE.md"); after != before {
		t.Fatalf("an unlisted pointer file is left as it is:\nbefore %q\nafter  %q", before, after)
	}
	if got, ok := cc.ReadFile("AGENTS.md"); !ok || !strings.Contains(got, ".lore/LORE.md") {
		t.Fatalf("the listed target is wired: %q", got)
	}
	// Listed again: no note.
	cc.MustLore("policy", "set", "context.targets", "codex,claude_code", "--yes")
	cc.MustLore("render").Never(t, "does not name it")
}
