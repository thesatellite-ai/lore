//go:build journeys && !windows

package journeys

// agentstop_test.go — a person stops an agent in a plain one-repo team (no
// org). devco:S-5: Kim's desk clone hands work to an agent started in her
// laptop clone before either side synced; the refusal says to sync on both
// sides, and after the syncs the handoff lands. devco:S-14: Kim stops an
// agent by its label, the agent's next write is refused with
// E_STATUS_CHANGE, and its open tasks move to her with reassign --from.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

var stopSession = regexp.MustCompile(`LORE_SESSION=(\S+)`)

func TestJourneyAgentStop(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kim := w.Persona("Kim Sato", "kim@stopco.example")
	lap, desk := kim.Laptop(), kim.NewLaptop("desk")
	remote := w.Remote("github.com/stop-co/app")
	var c, d *teamharness.Clone

	// agentAs starts label in clone x and returns the clone as that agent.
	agentAs := func(x *teamharness.Clone, label string) *teamharness.Clone {
		t.Helper()
		r := x.Lore("agent", "start", "--label", label, "--yes").OK(t)
		m := stopSession.FindStringSubmatch(r.Out())
		if m == nil {
			t.Fatalf("agent start printed no session: %s", r)
		}
		return x.With("LORE_HUMAN=", "LORE_ACTOR=agent:"+label, "LORE_SESSION="+m[1])
	}

	w.Step("day1/a-repo-with-no-org", func() {
		dir := filepath.Join(w.Root, "work", lap.Name, "app")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		c = lap.OpenClone(remote, dir)
		c.Git("init", "-q", "-b", "main")
		c.Git("remote", "add", "origin", remote.URL())
		jWrite(t, filepath.Join(dir, "main.go"), "package main\n")
		c.CommitAll("first")
		c.Git("push", "-q", "-u", "origin", "main")
		lap.LoreIn(c, "init").OK(t)
		lap.LoreIn(c, "actor", "register").OK(t)
		c.CommitAll("lore")
		if r := c.TryGit("push", "-q", "origin", "main"); r.Code != 0 {
			c.Git("push", "-q", "origin", "main") // the hook added the landing commit
		}
		d = desk.Clone(remote, "app")
		desk.LoreIn(d, "sync").OK(t)
	})

	w.Step("day1/handoff-to-an-agent-started-in-the-other-clone", func() {
		agentAs(c, "lead-codex-1")
		desk.LoreIn(d, "handoff", "add", "--for", "@lead-codex-1", "--body", "the retry work is yours", "--yes").
			Fails(t, teamharness.ExitNotFound, "E_NOT_FOUND", "they run `lore sync`, then you run `lore sync`")
		lap.LoreIn(c, "sync").OK(t)
		desk.LoreIn(d, "sync").OK(t)
		desk.LoreIn(d, "handoff", "add", "--for", "@lead-codex-1", "--body", "the retry work is yours", "--yes").OK(t)
	})

	w.Step("day2/kim-stops-an-agent-by-its-label", func() {
		bot := agentAs(c, "agent-lead-2")
		lap.LoreIn(c, "task", "add", "--title", "tidy the logger", "--commitment", "accepted", "--assign", "@agent-lead-2").OK(t)
		bot.Lore("memory", "add", "--body", "the logger is in pkg/log", "--yes").OK(t)

		lap.LoreIn(c, "actor", "deactivate", "--handle", "@agent-lead-2", "--yes").OK(t).
			Says(t, "deactivated", "lore task reassign --from @agent-lead-2").Never(t, "offboard")
		bot.Lore("memory", "add", "--body", "one more note", "--yes").
			Fails(t, teamharness.ExitRefused, "E_STATUS_CHANGE", "agent @agent-lead-2", "lore agent start --label agent-lead-2")
		lap.LoreIn(c, "task", "reassign", "--from", "@agent-lead-2", "--assign", "@kim", "--yes").OK(t).Says(t, "1 tasks reassigned to @kim")
		lap.LoreIn(c, "actor", "deactivate", "--handle", "@agent-lead-2", "--yes").
			Fails(t, teamharness.ExitRefused, "E_STATUS_CHANGE", "already deactivated")

		// the owner starts it again and it writes once more
		bot = agentAs(c, "agent-lead-2")
		bot.Lore("memory", "add", "--body", "back at work", "--yes").OK(t)
	})
}
