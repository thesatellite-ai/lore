//go:build journeys && !windows

package journeys

// live_compact_waiting_test.go — devco:D-881 (bug hunt H-140): with
// live.autopush off (the default), `lore live compact --yes` first pushed
// the live writes that wait for lore sync, then force-pushed the rewrite:
// a push nobody asked for, and in the leak cleanup (a row written by
// mistake, never synced, then purged) the leaked text reached the remote's
// history on the way. Now, while the remote has no live ref, the
// compaction stays on this machine and `lore sync` sends one clean commit;
// while the remote has one and writes wait, compact refuses (E_REFUSED)
// and names `lore sync run`; with nothing waiting it pushes its own
// rewrite, as its question said.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const jLcwSlug = "github.com/acme-co/compact-waiting-app"

func TestJourneyLiveCompactNeverPushesWaitingWrites(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	remote := w.Remote(jLcwSlug)
	parent := filepath.Join(lee.Home, "code")
	repo := jPlainRepo(t, lee, remote, filepath.Join(parent, "compact-waiting-app"))
	const ref = "refs/lore/live"
	remoteCommits := func() string {
		t.Helper()
		return strings.TrimSpace(w.TryGit(remote.Dir, "rev-list", "--count", ref).Stdout)
	}

	w.Step("day1/compact-before-any-sync-stays-here", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(repo.Dir, "init", "--non-interactive", "--name=compactwait").OK(t)
		repo.CommitAll("lore init")
		repo.Git("push", "-q", "origin", "HEAD:main")
		for _, title := range []string{"Rotate the staging certs", "Chase the unpaid invoice"} {
			jAgTaskID(t, lee, repo.Dir, title)
		}
		if remote.Has(ref) {
			t.Fatalf("autopush is off, yet the code remote holds %s before lore sync", ref)
		}
		lee.LoreIn(repo, "live", "compact", "--dry-run").OK(t).Says(t, "on this machine only", "lore sync run")
		lee.LoreIn(repo, "live", "compact").Fails(t, teamharness.ExitNeedsConfirm, "[E_NEEDS_CONFIRM]", "on this machine only")
		lee.LoreIn(repo, "live", "compact", "--yes").OK(t).
			Says(t, "compacted refs/lore/live to 1 commit", "on this machine only", "`lore sync run` sends it").
			Never(t, "force-pushed")
		if remote.Has(ref) {
			t.Fatalf("live compact --yes pushed the waiting live writes: the code remote now holds %s", ref)
		}
	})

	w.Step("day1/sync-sends-one-clean-commit", func() {
		lee.LoreIn(repo, "sync").OK(t)
		if !remote.Has(ref) || remoteCommits() != "1" {
			t.Fatalf("after lore sync the code remote holds %s with %q commits, want 1", ref, remoteCommits())
		}
		lee.LoreIn(repo, "task", "list").OK(t).Says(t, "Rotate the staging certs", "Chase the unpaid invoice")
	})

	w.Step("day2/a-waiting-write-on-a-shared-ref-is-refused", func() {
		jAgTaskID(t, lee, repo.Dir, "Renew the domain")
		jAgTaskID(t, lee, repo.Dir, "Close the old tickets")
		before := remote.Rev(ref)
		lee.LoreIn(repo, "live", "compact", "--yes").Fails(t, teamharness.ExitRefused,
			"[E_REFUSED]", "wait for `lore sync run`", "nothing changed")
		lee.LoreIn(repo, "live", "compact", "--dry-run").Fails(t, teamharness.ExitRefused, "[E_REFUSED]")
		if after := remote.Rev(ref); after != before {
			t.Fatalf("a refused live compact moved %s on the code remote: %s → %s", ref, before, after)
		}
	})

	w.Step("day2/after-sync-the-rewrite-goes-out", func() {
		lee.LoreIn(repo, "sync").OK(t)
		before := remote.Rev(ref)
		lee.LoreIn(repo, "live", "compact", "--yes").OK(t).Says(t, "force-pushed it to the remote origin")
		if after := remote.Rev(ref); after == before || remoteCommits() != "1" {
			t.Fatalf("live compact --yes with nothing waiting: %s %s → %s (%s commits)", ref, before, after, remoteCommits())
		}
	})
}
