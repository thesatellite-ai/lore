//go:build journeys && !windows

package journeys

// rosterauthority_test.go — who may change the roster (devco R-5, R-6).
// Lee leads the team; Ann and Bob are members. Ann's lore refuses her
// offboard of Bob. Ann then edits her own stint by hand in a plain clone
// of the store (org role admin) and pushes it past every hook; Lee's
// lore holds the change back, names the commit and its author, and
// keeps reading Ann as a member.

import (
	"strings"
	"testing"
)

func TestJourneyRosterAuthority(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)

	w.Step("day1/team-with-two-members", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "member", "add", "--email", tlAnn.Email, "--name", tlAnn.Name, "--handle", "ann", "--role", "member", "--yes").OK(t)
		tt.lore(tlLee, "member", "add", "--email", tlBob.Email, "--name", tlBob.Name, "--handle", "bob", "--role", "member", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		tlFirstSteps(t, ann, dir, tlTeamSetup, "member", tlAnn)
		tlApply(t, ann, dir, tlTeamSetup, "join", map[string]any{"invite": tt.store.SSHURL()})
		tlLastSteps(t, ann, dir, tlTeamSetup, []string{tlFolders})
		tt.lore(tlAnn, "sync").OK(t)
	})

	w.Step("day1/a-member-cannot-offboard", func() {
		tt.lore(tlAnn, "actor", "offboard", "--actor", "@bob", "--reason", "x", "--yes").Refused(t).
			Says(t, "only an admin of the org can offboard a teammate", "nothing was written", "lore member remove --who")
		tt.lore(tlLee, "member", "show", tlBob.Email).OK(t).Says(t, "active")
	})

	w.Step("day1/a-hand-made-promotion-is-held", func() {
		ann := tt.m[tlAnn.Email]
		hand := ann.Clone(tt.store, "hand")
		annID := tt.roster(t, tlLee)["human:"+tlAnn.Email].ID
		files := strings.Fields(hand.Git("grep", "-l", annID, "--", "*actor_periods*"))
		if len(files) != 1 {
			t.Fatalf("Ann's stint files: %v", files)
		}
		body, _ := hand.ReadFile(files[0])
		promoted := strings.Replace(body, `"org_role": "member"`, `"org_role": "admin"`, 1)
		if promoted == body {
			t.Fatalf("no member org role in Ann's stint: %s", body)
		}
		hand.WriteFile(files[0], promoted)
		hand.Git("commit", "-q", "--no-verify", "-am", "roster fix")
		hand.Git("push", "-q", "origin", "HEAD")
		short := hand.Git("rev-parse", "--short=7", "HEAD")
		tt.lore(tlLee, "sync").OK(t).Says(t, "held back in", "commit "+short+" by "+tlAnn.Email, "makes its author an admin and the author is not an admin", "the hold ends")
		tt.lore(tlLee, "member", "list").OK(t).Never(t, "admin   @ann")
		tt.lore(tlLee, "member", "show", tlAnn.Email, "--history").OK(t).Says(t, "roster fix", "[held back]")
	})
}
