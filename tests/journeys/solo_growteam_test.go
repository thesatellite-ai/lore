//go:build journeys && !windows

package journeys

// solo_growteam_test.go — a solo developer grows into a team (round 6
// track P). Sam keeps personal notes in a personal store, writes rules
// for a docs-only repo that must stay free of lore files, then starts an
// org and shares those notes:
//
//   - a personal rule written with --to me in a repo with no lore data is
//     listed and searched there; every read refused "lore isn't set up"
//     and the rule showed only in other repos (devco P-4);
//   - right after `lore org create`, the creator adds the first teammate;
//     member add refused "no setup is active here" (devco P-5);
//   - `lore org link` of a repo the org's own: pattern matches links it as
//     an own repo; it marked it a CLIENT repo and turned the leak guards on
//     (devco P-6);
//   - the personal rule moves to the org folder; F15 refused it because its
//     project_id named the personal store's project row (devco P-7);
//   - lore store init --name me run in that repo leaves it byte-identical;
//     it left an empty .lore/ (devco D-282).

import (
	"path/filepath"
	"regexp"
	"testing"
)

func TestJourneySoloGrowsIntoTeam(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona("Sam Okoro", "sam@acme.example")
	lap := sam.Laptop()
	personal := w.Remote("github.com/sam-okoro/notes")
	team := w.Remote("github.com/acme-co/team-store")
	app := lap.Clone(w.Remote("github.com/acme-co/app"), "app")
	skills := jPlainRepo(t, lap, w.Remote("github.com/acme-co/skills"), filepath.Join(lap.Home, "code", "skills"))
	var rid string

	w.Step("day1/personal-rule-in-a-repo-with-no-lore-data-reads-there", func() {
		// devco D-282: making the personal store from the docs repo set
		// nothing up there, yet left an empty .lore/ behind; it and a
		// second run (the store is recognised) leave the tree identical
		before := nlTreeHash(t, skills.Dir)
		lap.LoreIn(skills, "store", "init", "--name", "me", "--url", personal.SSHURL(), "--yes").OK(t)
		nlSame(t, "lore store init --name me", skills.Dir, before)
		lap.LoreIn(skills, "store", "init", "--name", "me", "--url", personal.SSHURL(), "--yes").OK(t)
		nlSame(t, "lore store init --name me (again)", skills.Dir, before)
		out := lap.LoreIn(skills, "rule", "add", "--body", "docs never hard-wrap a paragraph", "--to", "me").OK(t)
		m := regexp.MustCompile(`rul_[0-9a-f]{32}`).FindString(out.Out())
		if m == "" {
			t.Fatalf("no rule id in: %s", out.Out())
		}
		rid = m
		lap.LoreIn(skills, "rule", "list").OK(t).Says(t, "docs never hard-wrap a paragraph", "[me]")
		lap.LoreIn(skills, "search", "hard-wrap").OK(t).Says(t, rid)
		if st := skills.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("reads in the docs repo left files there:\n%s", st)
		}
	})

	w.Step("day2/org-creator-adds-the-first-teammate", func() {
		lap.LoreIn(app, "store", "init", "--name", "acme", "--url", team.SSHURL()).OK(t)
		lap.LoreIn(app, "org", "create", "--org", "acme", "--store", "acme", "--own", "github.com/acme-co/*",
			"--key", "human:sam@acme.example", "--name", "Sam Okoro", "--handle", "sam", "--yes").OK(t).Says(t, "acme's admin")
		lap.LoreIn(app, "member", "add", "--email", "ines@acme.example", "--name", "Ines Duarte", "--handle", "ines", "--role", "member", "--yes").
			OK(t).Says(t, "added ines@acme.example").Never(t, "no setup is active")
	})

	w.Step("day2/linking-an-own-repo-keeps-it-own", func() {
		lap.LoreIn(skills, "org", "link", "acme", "--project", "skills", "--yes").OK(t).
			Says(t, "OWN repos", "own: patterns").Never(t, "CLIENT", "leak guards turn on")
		lap.LoreIn(skills, "whoami").OK(t).Says(t, "own repo").Never(t, "client repo")
		if st := skills.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("org link wrote into the docs repo:\n%s", st)
		}
	})

	w.Step("day2/the-personal-rule-is-shared-with-the-org", func() {
		lap.LoreIn(skills, "move", "--id", rid, "--to", "acme/skills", "--yes").OK(t).Never(t, "F15")
		lap.LoreIn(skills, "rule", "list").OK(t).Says(t, rid+" ", "[acme/skills]")
	})
}
