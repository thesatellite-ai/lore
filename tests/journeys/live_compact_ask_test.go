//go:build journeys && !windows

package journeys

// live_compact_ask_test.go — devco:D-751 (bug hunt H-10): lore live
// compact, run with no terminal and without --yes, force-pushed a
// rewritten refs/lore/live to the code repo's remote (the ref every
// teammate's clone fetches) with live.autopush off, asked nothing, and
// neither its help nor its output said the remote changed. Now it plans,
// answers E_NEEDS_CONFIRM naming the rewrite and the remote, and leaves
// the remote ref as it was; with --yes it compacts and says it
// force-pushed to the remote; --dry-run names the remote and changes
// nothing.

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const jLcaSlug = "github.com/acme-co/compact-app"

func TestJourneyLiveCompactAsksBeforeRewritingTheRemote(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	remote := w.Remote(jLcaSlug)
	parent := filepath.Join(lee.Home, "code")
	repo := jPlainRepo(t, lee, remote, filepath.Join(parent, "compact-app"))
	const ref = "refs/lore/live"

	w.Step("day1/three-live-tasks-shared", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(repo.Dir, "init", "--non-interactive", "--name=compact").OK(t)
		repo.CommitAll("lore init")
		repo.Git("push", "-q", "origin", "HEAD:main")
		for _, title := range []string{"Rotate the staging certs", "Renew the domain", "Close the old tickets"} {
			jAgTaskID(t, lee, repo.Dir, title)
		}
		lee.LoreIn(repo, "sync").OK(t)
		if !remote.Has(ref) {
			t.Fatalf("the code remote holds no %s after lore sync", ref)
		}
	})

	w.Step("day2/compact-without-yes-asks-and-changes-nothing", func() {
		before := remote.Rev(ref)
		lee.LoreIn(repo, "live", "compact", "--dry-run").OK(t).Says(t, "force-push it to the remote origin")
		lee.LoreIn(repo, "live", "compact").Fails(t, teamharness.ExitNeedsConfirm,
			"[E_NEEDS_CONFIRM]", "force-push it to the remote origin that every teammate's clone fetches", "--yes")
		if after := remote.Rev(ref); after != before {
			t.Fatalf("live compact without --yes rewrote %s on the code remote: %s → %s", ref, before, after)
		}
	})

	w.Step("day2/compact-with-yes-rewrites-and-says-so", func() {
		before := remote.Rev(ref)
		lee.LoreIn(repo, "live", "compact", "--yes").OK(t).
			Says(t, "compacted refs/lore/live to 1 commit", "force-pushed it to the remote origin", "backup kept locally")
		if after := remote.Rev(ref); after == before {
			t.Fatalf("live compact --yes left %s on the code remote unchanged", ref)
		}
		lee.LoreIn(repo, "live", "compact").OK(t).Says(t, "nothing to compact")
	})
}
