//go:build journeys && !windows

package journeys

// revoke_fence_test.go — a lead takes a task back from a teammate's
// coding agent (devco:S-12, S-13, S-17, S-20). The revoke pushes only
// with --push; the agent's next write says who revoked it and when; its
// task next picks other work and a direct claim is refused until the lead
// reassigns; the cap on claims is a team policy the agent cannot lift
// with its own config.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyRevokeFencesAgent(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lead := w.Persona("Ines Corva", "ines@gearbox.example").Laptop()
	dev := w.Persona("Otto Brann", "otto@gearbox.example").Laptop()
	app := w.Remote("github.com/gearbox-dev/app")
	lead.Actor.SetEnv(teamharness.EnvPushClaims, "0") // no standing go-ahead: each push is asked for
	dev.Actor.SetEnv(teamharness.EnvPushClaims, "0")
	var lc, dc *teamharness.Clone
	var ids []string
	const sess = "sess-otto-agent-0001"
	agent := func(args ...string) teamharness.Result { return jAgentsRun(dev, dc, &jClaude, sess, args...) }

	w.Step("day1/lead-plans-three-tasks", func() {
		lc = lead.Clone(app, "app")
		lead.LoreIn(lc, "init").OK(t)
		lc.CommitAll("lore init")
		lc.Git("push", "-q", "origin", "HEAD")
		for _, title := range []string{"wire the gear api", "draw the gear ui", "write the gear docs"} {
			ids = append(ids, asTaskID.FindString(lead.LoreIn(lc, "task", "add", "--title", title, "--priority", "high").OK(t).Out()))
		}
		lead.LoreIn(lc, "sync").OK(t)
		dc = dev.Clone(app, "app")
		dev.LoreIn(dc, "sync").OK(t)
		agent("task", "claim", "--id", ids[0], "--push").OK(t).Says(t, "claimed "+ids[0])
	})

	w.Step("day1/revoke-pushes-only-when-asked", func() {
		lead.LoreIn(lc, "task", "revoke", "--id", ids[0]).
			Refused(t, "a revoke here is shared through refs/lore/signals", "nothing was written", "--push")
		agent("task", "note", "--id", ids[0], "--text", "still mine after a refused revoke").OK(t)
		lead.LoreIn(lc, "task", "revoke", "--id", ids[0], "--push").OK(t).
			Says(t, "revoked the claim on "+ids[0], "pushed refs/lore/signals to origin", "fenced out", "lore task reassign")
	})

	w.Step("day1/agent-is-told-who-and-kept-off", func() {
		agent("task", "done", "--id", ids[0], "--no-lesson").Fails(t, 12, "was revoked by Ines Corva at").Never(t, "expired")
		next := agent("task", "next", "--push").OK(t).Never(t, "claimed "+ids[0])
		got := asTaskID.FindString(next.Out())
		if got == "" || got == ids[0] {
			t.Fatalf("task next after a revoke must claim other work:\n%s", next)
		}
		agent("task", "release", "--id", got).OK(t)
		agent("task", "next", "--dry-run").OK(t).Says(t, ids[0], "revoked from you by")
		agent("task", "claim", "--id", ids[0], "--push").Refused(t, "revoked from you by", "lore task reassign --id "+ids[0])
	})

	w.Step("day2/the-claim-cap-is-a-team-policy", func() {
		agent("task", "claim", "--id", ids[1], "--push").OK(t)
		agent("task", "claim", "--id", ids[2], "--push").
			Refused(t, "ask a person to raise the team limit (lore policy set --key agents.max_claims_per_agent --value 2)").
			Never(t, "lore config set")
		agent("config", "set", "--key", "agents.max_claims_per_agent", "--value", "2").OK(t)
		agent("task", "claim", "--id", ids[2], "--push").Refused(t, "agents.max_claims_per_agent 1")
		// A policy row is content: it reaches the team with a commit and a push.
		lead.LoreIn(lc, "policy", "set", "--key", "agents.max_claims_per_agent", "--value", "2", "--yes").OK(t)
		lc.CommitAll("cap agent claims at two")
		lc.Git("push", "-q", "origin", "HEAD")
		dc.Git("pull", "-q", "--ff-only")
		dev.LoreIn(dc, "policy", "show").OK(t).Says(t, "agents.max_claims_per_agent\t2")
		agent("task", "claim", "--id", ids[2], "--push").OK(t).Says(t, "claimed "+ids[2])
	})

	w.Step("day2/reassign-lifts-the-fence", func() {
		agent("task", "release", "--id", ids[2]).OK(t)
		lead.LoreIn(lc, "actor", "register").OK(t)
		lead.LoreIn(lc, "task", "reassign", "--id", ids[0], "--assign", "@ines").Refused(t, "--push")
		lead.LoreIn(lc, "task", "reassign", "--id", ids[0], "--assign", "@ines", "--push").OK(t)
		agent("task", "claim", "--id", ids[0], "--push").OK(t).Says(t, "claimed "+ids[0])
	})
}

// TestJourneySoloRepoClaimsLocally (devco:D-362): in a repo with no remote
// an agent's task next (with or without --push) failed E_OFFLINE
// "autopush is off", so a solo agent could not claim at all. The claim is
// local by nature: it works, says it stays in this repository, and the
// task is finished as usual.
func TestJourneySoloRepoClaimsLocally(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	solo := w.Persona("Mara Lind", "mara@sidequest.example").Laptop()
	unused := w.Remote("github.com/sidequest/notes") // bookkeeping only: never added as a remote
	solo.Actor.SetEnv(teamharness.EnvPushClaims, "0")
	var c *teamharness.Clone
	var id string
	const sess = "sess-mara-solo-0001"

	w.Step("day1/a-repo-with-no-remote", func() {
		dir := filepath.Join(w.Root, "work", solo.Name, "notes")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		c = solo.OpenClone(unused, dir)
		c.Git("init", "-q", "-b", "main")
		jWrite(t, filepath.Join(dir, "README.md"), "notes\n")
		c.CommitAll("first")
		solo.LoreIn(c, "init").OK(t)
		id = asTaskID.FindString(solo.LoreIn(c, "task", "add", "--title", "tidy the side quest notes").OK(t).Out())
	})

	w.Step("day1/the-agent-claims-locally", func() {
		jAgentsRun(solo, c, &jClaude, sess, "task", "next").OK(t).
			Says(t, "claimed "+id, "local: this repo has no remote").Never(t, "E_OFFLINE", "autopush")
		jAgentsRun(solo, c, &jClaude, sess, "task", "next", "--push").OK(t).Says(t, "you hold "+id)
		jAgentsRun(solo, c, &jClaude, sess, "task", "list").OK(t).Says(t, id, "working: ") // the board reads the local claim
		jAgentsRun(solo, c, &jClaude, sess, "task", "done", "--id", id, "--no-lesson").OK(t).Says(t, id+" done")
	})
}
