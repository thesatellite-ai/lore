//go:build journeys && !windows

package journeys

// staff_remove_test.go — removing a member (B2-11, B2-12, B2-14). Lee
// adds Ann on @web; Ann joins and writes to the org store. Lee removes
// her with `lore member remove --yes` (one run, no second question);
// she is off @web. On Ann's laptop, after `lore sync`, the next write to
// the org's store is refused saying she left, while her personal store
// still takes writes. Lee undoes the removal the same day with `lore
// member add`; after a sync Ann writes to the org store again.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// ownCommits counts ref's commits that are not merges of what a teammate
// pushed meanwhile (a push after someone else's write merges first, B1-21
// asks about the writes of one command).
func ownCommits(r *teamharness.Remote, ref string) int {
	n := 0
	for _, s := range r.Log(ref) {
		if s != "" && !strings.HasPrefix(s, "lore: merge") {
			n++
		}
	}
	return n
}

func TestJourneyStaffRemove(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	orgStore := tlOrg + "/org"
	mainRef := "refs/heads/main"
	liveRef := "refs/lore/live"

	w.Step("day1/lee-adds-ann", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "team", "add", "@web", "--members", "@lee").OK(t)
		tt.lore(tlLee, "member", "add", tlAnn.Email, "--name", tlAnn.Name, "--team", "@web", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t) // C5: the roster reaches the team store with Lee's sync
	})

	w.Step("day1/ann-joins-and-writes", func() {
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		tlFirstSteps(t, ann, dir, tlTeamSetup, "member", tlAnn)
		tlApply(t, ann, dir, tlTeamSetup, "join", map[string]any{"invite": tt.store.SSHURL()})
		tlLastSteps(t, ann, dir, tlTeamSetup, []string{tlFolders})
		tt.lore(tlAnn, "init").OK(t)
		tt.lore(tlAnn, "memory", "add", "ann's note before", "--to", orgStore, "--yes").OK(t)
	})

	w.Step("day1/lee-removes-ann", func() {
		before := ownCommits(tt.store, mainRef)
		tt.lore(tlLee, "member", "remove", tlAnn.Email, "--reason", "moved on", "--yes").OK(t).Says(t, "removed")
		tt.lore(tlLee, "sync").OK(t) // C5: the removal reaches the team store with Lee's sync
		// B1-21: the stint, the team row and the actor in ONE store commit
		if n := ownCommits(tt.store, mainRef) - before; n != 1 {
			t.Errorf("member remove wrote %d store commits, want one", n)
		}
		tt.lore(tlLee, "member", "show", tlAnn.Email).OK(t).Says(t, "left").Never(t, "@web")
		tt.lore(tlLee, "team", "show", "@web").OK(t).Never(t, tlAnn.Name)
	})

	w.Step("day1/ann-writes-after-removal-then-syncs", func() {
		// F-9: Ann has not synced since Lee removed her; what she writes now
		// is kept locally (writes commit locally until lore sync), and the
		// sync that brings the removal must not send it to the team's
		// remotes: it says why in one line and keeps the commits
		storeBefore, liveBefore := tt.store.Log(mainRef), tt.app.Log(liveRef)
		tt.lore(tlAnn, "memory", "add", "a repo note after leaving", "--to", "repo").OK(t)
		tt.lore(tlAnn, "memory", "add", "ann's note after leaving", "--to", orgStore, "--yes").OK(t)
		tt.lore(tlAnn, "sync").Says(t, "you are marked as left in "+tlOrg, "kept on this machine")
		if got := tt.store.Log(mainRef); len(got) != len(storeBefore) {
			t.Errorf("Ann's sync pushed to the team store after her removal:\n%v", got[:len(got)-len(storeBefore)])
		}
		if got := tt.app.Log(liveRef); len(got) != len(liveBefore) {
			t.Errorf("Ann's sync pushed refs/lore/live after her removal:\n%v", got[:len(got)-len(liveBefore)])
		}
	})

	w.Step("day1/ann-next-write-is-refused", func() {
		tt.lore(tlAnn, "sync").OK(t)
		tt.lore(tlAnn, "memory", "add", "ann's note after", "--to", orgStore, "--yes").
			Refused(t, "E_REFUSED", "you are marked as left in "+tlOrg, "lore member add")
		tt.lore(tlAnn, "memory", "add", "my own note", "--to", "me", "--yes").OK(t)
		// doctor says she left (and caches the answer for a minute)
		tt.lore(tlAnn, "doctor", "--all").Says(t, "no open period", "lore member add")
	})

	w.Step("day1/lee-undoes-it-the-same-day", func() {
		before := ownCommits(tt.store, mainRef)
		tt.lore(tlLee, "member", "add", tlAnn.Email, "--team", "@web", "--yes").OK(t).Says(t, "back on the roster")
		tt.lore(tlLee, "sync").OK(t) // C5: each change reaches the team store with Lee's sync
		if n := ownCommits(tt.store, mainRef) - before; n != 1 {
			t.Errorf("member add (restore) wrote %d store commits, want one", n)
		}
		before = ownCommits(tt.store, mainRef)
		tt.lore(tlLee, "member", "edit", tlAnn.Email, "--name", "Ann B", "--role", "pm", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		if n := ownCommits(tt.store, mainRef) - before; n != 1 {
			t.Errorf("member edit --name --role wrote %d store commits, want one", n)
		}
		tt.lore(tlLee, "member", "show", tlAnn.Email).OK(t).Says(t, "active", "@web")
		tt.lore(tlAnn, "sync").OK(t)
		// B2-24: right after the sync doctor reads the restored roster, not
		// its cached "left" answer
		tt.lore(tlAnn, "doctor", "--all").Says(t, "employee since").Never(t, "no open period")
		tt.lore(tlAnn, "memory", "add", "ann's note back", "--to", orgStore, "--yes").OK(t)
	})
}
