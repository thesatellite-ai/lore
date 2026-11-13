//go:build journeys && !windows

package journeys

// migrate_push_test.go — a lore 0.1.17 repo migrated, committed, pushed and
// checked by CI (user reports A-8, B-6, C-7). Rosa's old repo has a memory
// whose body has held a key-shaped value since lore 0.1.17, and the actor
// copy 0.1.17 wrote with its stable_key (an email). `lore migrate run`
// trims the actor copy to {id, display name, kind}; the migration commit
// passes (the value was already in that row at HEAD) and so does its push
// (the value was already in that row on the remote): the two checks agree.
// A fresh CI clone of the remote passes `lore check leaks --ci`. A truly
// new key is still refused at push, with a ready allow line in the one
// spelling every hint uses.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// migPushActor is the 0.1.17 actor copy of the journey's old repo.
const migPushActor = "act_01a1cb5743e371b7bab946c6cf48ab02"

func TestJourneyMigratePushAndCI(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	rosa := w.Persona("Rosa Lind", "rosa@widgets.example")
	lap := rosa.Laptop()
	old := "AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in the repo
	fresh := "AKIA" + strings.Repeat("Z", 16)
	remote := w.Remote("github.com/widgets/migpush")
	var c *teamharness.Clone
	actorPath := ".lore/data/actors/" + migPushActor + ".json"

	w.Step("day1/old-repo-with-a-key-and-a-full-actor-copy", func() {
		c = lap.Clone(remote, "migpush")
		c.WriteFile(actorPath, `{"_table": "actors", "_v": 1, "created_at": "2026-11-03T10:00:00.000000000Z", "display_name": "rosa", "id": "`+migPushActor+
			`", "kind": "human", "stable_key": "human:rosa@widgets.example", "updated_at": "2026-11-03T10:00:00.000000000Z"}`+"\n")
		fixCheckoutMode(t, c, actorPath)
		fixOldRepoWarned(t, c, old) // commits and pushes the 0.1.17 history
	})

	w.Step("day1/migrate-trims-the-actor-copy", func() {
		r := lap.LoreIn(c, "migrate", "run").OK(t)
		r.Says(t, "actor_projection:", "stable_key", "lore secrets allow --row-id <row> --field <field> --rule <rule>").Never(t, old)
		body, _ := c.ReadFile(actorPath)
		if strings.Contains(body, "stable_key") || strings.Contains(body, `"_o"`) {
			t.Fatalf("the migrated actor copy holds more than {id, display name, kind}:\n%s", body)
		}
	})

	w.Step("day1/commit-and-push-agree", func() {
		lap.LoreIn(c, "fix").OK(t) // wires git's hooks in this clone (a fresh clone has none)
		c.Git("add", "-A", ".lore")
		c.TryGit("commit", "-q", "-m", "lore: migrate rows to format 2").OK(t).Says(t, "already in the same row(s) at HEAD").Never(t, old)
		c.TryGit("push", "-q", "origin", "HEAD").OK(t).Says(t, "already in the same row(s) on the remote").Never(t, old)
		if r := c.TryGit("grep", "-l", `"_o"`, "HEAD", "--", ".lore/data/actors/"); r.Code == 0 {
			t.Fatalf("an actor copy lore wrote in the repo carries _o: %s", r)
		}
	})

	w.Step("day1/ci-clone-passes-check-leaks-ci", func() {
		ci := lap.Clone(remote, "migpush-ci")
		lap.LoreIn(ci, "check", "leaks", "--ci").OK(t)
	})

	w.Step("day1/a-new-key-is-still-refused-at-push", func() {
		path := ".lore/data/memories/" + fixWarnMemory + ".json"
		body, _ := c.ReadFile(path)
		c.WriteFile(path, strings.Replace(body, old, old+" and "+fresh, 1))
		fixCheckoutMode(t, c, path)
		c.Git("add", path)
		c.Git("commit", "-q", "--no-verify", "-m", "memory: second key")
		c.TryGit("push", "-q", "origin", "HEAD").Refused(t, "E_LEAK_BLOCKED", "not a secret? lore secrets allow --row-id "+fixWarnMemory+" --field body --rule ", "push again").Never(t, fresh)
	})
}
