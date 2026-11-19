//go:build journeys && !windows

package journeys

// memberagent_test.go — a member's own coding agent (devco U-1). Lee
// leads the team; Ann is a plain member who works through a coding
// agent. Her agent's first org write registers the agent (owner = Ann)
// in the same commit as the note it writes. Every reader used to hold
// that agent row back as a forged roster change, and each later write of
// Ann's agent then failed a bare E_STALE. Now her agent's writes reach
// Lee, nothing is held, and her second write goes through.
// devco U-2: Lee, one commit behind Ann's latest push, migrates an old
// lore 0.1.17 repo; the run takes Ann's push in on the way, and its
// summary no longer reports that as the run's own store upgrade, push or
// new actor row.

import (
	"path/filepath"
	"testing"
)

func TestJourneyMemberAgentWrites(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	org := tlOrg + "/org"

	w.Step("day1/team-with-a-member", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "member", "add", "--email", tlAnn.Email, "--name", tlAnn.Name, "--handle", "ann", "--role", "member", "--yes").OK(t)
		tt.lore(tlLee, "init").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		tlFirstSteps(t, ann, dir, tlTeamSetup, "member", tlAnn)
		tlApply(t, ann, dir, tlTeamSetup, "join", map[string]any{"invite": tt.store.SSHURL()})
		tlLastSteps(t, ann, dir, tlTeamSetup, []string{tlFolders})
		tt.lore(tlAnn, "sync").OK(t)
	})

	w.Step("day1/the-members-agent-writes-and-the-lead-reads", func() {
		ann, repo := tt.m[tlAnn.Email], tt.repo[tlAnn.Email]
		jAgentsRun(ann, repo, &jClaude, "s1", "memory", "add", "--body", "zephyr note one", "--to", org).OK(t)
		jAgentsRun(ann, repo, &jClaude, "s1", "sync").OK(t).Never(t, "held back")
		jAgentsRun(ann, repo, &jClaude, "s1", "memory", "add", "--body", "zephyr note two", "--to", org).OK(t).Never(t, "E_STALE")
		jAgentsRun(ann, repo, &jClaude, "s1", "sync").OK(t).Never(t, "held back")
		tt.lore(tlLee, "sync").OK(t).Never(t, "held back")
		tt.lore(tlLee, "search", "zephyr").OK(t).Says(t, "zephyr note one", "zephyr note two")
	})

	w.Step("day2/a-migration-takes-in-a-teammates-push", func() {
		ann, repo := tt.m[tlAnn.Email], tt.repo[tlAnn.Email]
		jAgentsRun(ann, repo, &jClaude, "s2", "memory", "add", "--body", "zephyr note three", "--to", org).OK(t)
		jAgentsRun(ann, repo, &jClaude, "s2", "sync").OK(t)
		old := tt.w.Remote("github.com/acme-co/oldapp")
		seed := ann.Clone(old, "oldapp-seed")
		fixOldRepo(t, seed)
		seed.Git("push", "-q", "origin", "HEAD")
		lee := tt.m[tlLee.Email]
		c := lee.Clone(old, "oldapp")
		lee.MoveClone(t, c, filepath.Join(lee.Home, "code", "oldapp"))
		lee.LoreIn(c, "migrate", "run").OK(t).Says(t, "files upgraded").
			Never(t, "store "+tlOrg, "the run's own writes added", ", pushed")
	})
}
