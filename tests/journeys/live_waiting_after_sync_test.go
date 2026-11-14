//go:build journeys && !windows

package journeys

// live_waiting_after_sync_test.go — doctor right after a sync says nothing
// waits to land when the sync left nothing to land (devco N-14). Ann's
// first live writes bring her actor row along as a companion row; a sync
// never lands a companion row alone (a plain task add must not move
// main), yet doctor said "1 row(s) waiting to land; the next live write or
// `lore sync` lands them" on Ann's and Bob's clones right after a sync
// with a real remote, and no sync cleared it. A finished task still lands
// with the companion row beside it.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyDoctorAfterSyncHasNothingWaitingToLand(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ann := w.Persona("Ann Able", "ann@example.test").Laptop()
	bob := w.Persona("Bob Brook", "bob@example.test").Laptop()
	remote := w.Remote("github.com/acme-co/app")
	a := jPlainRepo(t, ann, remote, filepath.Join(ann.Home, "code", "app"))
	var taskID string

	w.Step("day1/first-live-writes-then-sync", func() {
		ann.LoreIn(a, "init", "--yes").OK(t)
		a.CommitAll("lore init")
		a.Git("push", "-q", "origin", "HEAD:main")
		r := ann.LoreIn(a, "task", "add", "--title", "ship the invoice page", "--json").OK(t)
		if i := strings.Index(r.Stdout, "tsk_"); i >= 0 && len(r.Stdout) >= i+36 {
			taskID = r.Stdout[i : i+36]
		}
		ann.LoreIn(a, "sync").OK(t)
		ann.LoreIn(a, "doctor").Never(t, "waiting to land")
		ann.LoreIn(a, "sync", "status").OK(t).Never(t, "waiting to land")
	})

	w.Step("day1/teammate-clone-syncs", func() {
		b := bob.Clone(remote, "app")
		bob.MoveClone(t, b, filepath.Join(bob.Home, "code", "app"))
		bob.LoreIn(b, "sync").OK(t)
		bob.LoreIn(b, "doctor").Never(t, "waiting to land")
	})

	w.Step("day2/a-finished-task-lands-with-it", func() {
		if taskID == "" {
			t.Fatal("task add printed no task id")
		}
		ann.LoreIn(a, "task", "done", "--id", taskID).OK(t)
		ann.LoreIn(a, "sync").OK(t).Says(t, "landed on main")
		ann.LoreIn(a, "doctor").Never(t, "waiting to land")
	})
}
