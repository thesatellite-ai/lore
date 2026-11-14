//go:build journeys && !windows

package journeys

// slowremote_test.go — a slow remote stays out of the way (devco L-12-perf,
// L-12-land). Every ssh call to Sol's git host costs seconds, so lore makes
// as few as it can: a write fetches once when its fetch is due (its
// refusals need teammates' rows, D-233) and makes no call right after, and
// a sync that also lands a finished task makes one fetch and one push to
// the code remote. The fake
// ssh logs each call, so the counts are checked, never wall times.
//
//	day 1  online: init, commit and push; origin moves to the slow ssh host
//	day 2  after a pause: a task add fetches once and pushes nothing; a
//	       task start right after makes no call; the task is finished; lore sync fetches once and pushes once (the live
//	       writes and the landing commit in one push)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// slowSSH is a fake ssh that logs the command git asks the host to run
// (git-upload-pack is a fetch, git-receive-pack a push) and runs it here.
const slowSSH = "#!/bin/sh\nfor a; do cmd=$a; done\necho \"$cmd\" >> \"$FAKE_SSH_LOG\"\nexec sh -c \"$cmd\"\n"

func TestJourneySlowRemote(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sol := w.Persona("Sol Slowlink", "sol@slowlink.example")
	lap := sol.Laptop()
	site := w.Remote("github.com/sol-dev/site")
	repo := lap.Clone(site, "site")
	log := filepath.Join(t.TempDir(), "ssh-calls")
	// calls returns the fetches and pushes logged since the last call. The
	// invariants run lore doctor (a read, which fetches) after each step,
	// so each step counts from its own start.
	var last string // the calls the last count read, for a failure message
	calls := func() (fetches, pushes int) {
		b, _ := os.ReadFile(log) // no file yet: no call
		_ = os.Remove(log)
		last = string(b)
		return strings.Count(string(b), "git-upload-pack"), strings.Count(string(b), "git-receive-pack")
	}

	w.Step("day1/online-start", func() {
		lap.LoreIn(repo, "init").OK(t)
		repo.CommitAll("add lore")
		repo.Git("push", "-q", "origin", "HEAD")
		bin := fakeexec.Write(t, filepath.Join(t.TempDir(), "ssh"), slowSSH, 0o755)
		lap.SetEnv("GIT_SSH_COMMAND", bin)
		lap.SetEnv("GIT_SSH_VARIANT", "simple")
		lap.SetEnv("FAKE_SSH_LOG", log)
		// origin keeps its address (the repo record stays as it is); only
		// this clone's git reaches it over the slow ssh host
		origin := strings.TrimSpace(repo.Git("config", "--get", "remote.origin.url")) // as typed, not as insteadOf rewrites it
		owner := origin[:strings.LastIndex(origin, "/")+1]
		repo.Git("config", "url.ssh://slowhost"+filepath.Dir(site.Dir)+"/.insteadOf", owner)
		lap.LoreIn(repo, "sync").OK(t)
		if fe, _ := calls(); fe == 0 {
			t.Error("the fake ssh logged no call: the counts below would prove nothing")
		}
	})

	var id string
	w.Step("day2/a-write-fetches-once-when-due", func() {
		w.AdvanceDays(1) // long past live.fetch_ttl
		calls()          // the invariants' own reads (lore doctor) after the last step
		r := lap.LoreIn(repo, "task", "add", "ship the slow-link page").OK(t)
		if fe, pu := calls(); fe != 1 || pu != 0 {
			t.Errorf("task add after a pause made %d fetch(es) and %d push(es), want 1 and 0:\n%s\n%s", fe, pu, last, r.Out())
		}
		for _, f := range strings.Fields(r.Out()) {
			if strings.HasPrefix(f, "tsk_") {
				id = f
				break
			}
		}
		if id == "" {
			t.Fatalf("no task id in:\n%s", r.Out())
		}
		lap.LoreIn(repo, "task", "start", "--id", id).OK(t)
		if fe, pu := calls(); fe+pu != 0 {
			t.Errorf("task start right after a fetch made %d fetch(es) and %d push(es), want none:\n%s", fe, pu, last)
		}
		// task done reads the claims on the remote's refs/lore/signals first
		// (a task a teammate claimed is never closed unfenced, B11): that
		// one fetch is the done's, not counted against the sync below
		lap.LoreIn(repo, "task", "done", "--id", id).OK(t)
		if _, pu := calls(); pu != 0 {
			t.Errorf("task done pushed %d time(s), want none", pu)
		}
	})

	w.Step("day2/sync-fetches-once-pushes-once", func() {
		calls() // the invariants' own reads after the last step
		r := lap.LoreIn(repo, "sync").OK(t).Says(t, "landed on main")
		if fe, pu := calls(); fe != 1 || pu != 1 {
			t.Errorf("a landing sync made %d fetch(es) and %d push(es), want 1 and 1:\n%s\n%s", fe, pu, last, r.Out())
		}
		if len(site.FilesMatching("refs/heads/main", id)) == 0 {
			t.Errorf("the finished task did not land on main:\n%s", r.Out())
		}
		if local := strings.TrimSpace(repo.Git("rev-parse", "refs/lore/live")); site.Rev("refs/lore/live") != local {
			t.Errorf("the remote live ref is not this clone's (%s):\n%s", local, r.Out())
		}
	})
}
