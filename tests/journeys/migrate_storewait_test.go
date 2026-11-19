//go:build journeys && !windows

package journeys

// migrate_storewait_test.go — a `lore migrate run` that has nothing to
// migrate moves no remote (devco:D-521; only push and sync move a
// remote). Lee's team store is on the default push mode (manual): a task
// he files into it waits, committed on his laptop. In the code repo linked
// to the store's folder he runs `lore migrate run`: every row is current,
// so the run says "Nothing to migrate" and the store's remote main stays
// where it was; the waiting commit still goes out with `lore sync`.

import (
	"path/filepath"
	"testing"
)

const jMigWaitSlug = "github.com/acme-co/wait-web"

func TestJourneyMigrateNothingPushesNoWaitingStoreCommit(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	mainRef := "refs/heads/main"
	var web string

	w.Step("day1/team-and-a-linked-repo", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", "wait_web").OK(t)
		c := lee.Clone(w.Remote(jMigWaitSlug), "wait-web")
		lee.MoveClone(t, c, filepath.Join(parent, "wait-web"))
		lee.LoreIn(c, "init", "--non-interactive", "--name=wait-web").OK(t)
		lee.LoreIn(c, "org", "link", tlOrg, "--project", "wait_web", "--yes").OK(t)
		web = c.Dir
		lee.Lore(web, "sync").OK(t)
	})

	w.Step("day2/a-task-waits-and-migrate-moves-nothing", func() {
		w.AdvanceDays(1)
		lee.Lore(web, "task", "add", "Waiting task", "--to", tlOrg).OK(t).Says(t, "committed locally")
		before := tt.store.Rev(mainRef)
		lee.Lore(web, "migrate", "run").OK(t).
			Says(t, "Nothing to migrate").
			Never(t, "pushed store", "pushed")
		if after := tt.store.Rev(mainRef); after != before {
			t.Fatalf("lore migrate run with nothing to migrate moved the store's main: %s -> %s", before, after)
		}
	})

	w.Step("day2/sync-still-sends-it", func() {
		before := tt.store.Rev(mainRef)
		lee.Lore(web, "sync").OK(t)
		if tt.store.Rev(mainRef) == before {
			t.Fatal("lore sync did not send the waiting store commit")
		}
	})

	// push=now: a write pushes at once; one made offline waits, and a
	// migration with nothing to migrate still leaves it waiting
	w.Step("day3/push-now-store-commit-that-failed-offline", func() {
		w.AdvanceDays(1)
		lee.Lore(parent, "store", "set", tt.store.SSHURL(), "push=now").OK(t)
		lee.Lore(web, "sync").OK(t)
		lee.Offline()
		lee.Lore(web, "task", "add", "Offline task", "--to", tlOrg).OK(t)
		lee.Online()
		w.AdvanceDays(1)
		before := tt.store.Rev(mainRef)
		lee.Lore(web, "migrate", "run").OK(t).
			Says(t, "Nothing to migrate").
			Never(t, "pushed store")
		if after := tt.store.Rev(mainRef); after != before {
			t.Fatalf("lore migrate run with nothing to migrate pushed a push=now store's waiting commit: %s -> %s", before, after)
		}
		lee.Lore(web, "sync").OK(t)
		if tt.store.Rev(mainRef) == before {
			t.Fatal("lore sync did not send the waiting store commit")
		}
	})
}
