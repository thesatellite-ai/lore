//go:build journeys && !windows

package journeys

// offline_backoff_test.go — a day on a network that times out (devco:U-6).
// Every ssh call to Kit's git host waits for a connect timeout and fails.
// lore tries the remote once, records the failure and backs off for
// live.fetch_ttl: the next writes and reads make no call at all, and
// `lore sync` (which always tries) is the way to reach the remote sooner.
// Before, every command fetched twice (the failed fetch was retried and
// left no stamp): 41 s writes and 21 s reads all day.
//
//	day 1  online: init, commit and push; origin moves to the timing-out host
//	day 2  a write tries the remote once; reads and writes after it make no call;
//	       lore sync tries again

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// timeoutSSH is a fake ssh that logs each call, waits past
// gitremote.RetryAfter's limit like a real connect timeout and fails the
// way ssh does.
const timeoutSSH = "#!/bin/sh\necho call >> \"$FAKE_SSH_LOG\"\nsleep 2.2\necho \"ssh: connect to host timeouthost port 22: Connection timed out\" >&2\nexit 255\n"

func TestJourneyOfflineBackoff(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kit := w.Persona("Kit Trainride", "kit@trainride.example")
	lap := kit.Laptop()
	site := w.Remote("github.com/kit-dev/notes")
	repo := lap.Clone(site, "notes")
	log := filepath.Join(t.TempDir(), "ssh-calls")
	calls := func() int {
		b, _ := os.ReadFile(log) // no file yet: no call
		_ = os.Remove(log)
		return strings.Count(string(b), "call")
	}

	w.Step("day1/online-start", func() {
		lap.LoreIn(repo, "init").OK(t)
		repo.CommitAll("add lore")
		repo.Git("push", "-q", "origin", "HEAD")
		lap.LoreIn(repo, "sync").OK(t)
		bin := fakeexec.Write(t, filepath.Join(t.TempDir(), "ssh"), timeoutSSH, 0o755)
		lap.SetEnv("GIT_SSH_COMMAND", bin)
		lap.SetEnv("GIT_SSH_VARIANT", "simple")
		lap.SetEnv("FAKE_SSH_LOG", log)
		origin := strings.TrimSpace(repo.Git("config", "--get", "remote.origin.url"))
		owner := origin[:strings.LastIndex(origin, "/")+1]
		repo.Git("config", "url.ssh://timeouthost"+filepath.Dir(site.Dir)+"/.insteadOf", owner)
	})

	w.Step("day2/one-try-then-back-off", func() {
		w.AdvanceDays(1) // long past live.fetch_ttl: the first command tries
		calls()          // the invariants' own reads after the last step
		r := lap.LoreIn(repo, "memory", "add", "--body", "written on the train").OK(t)
		if n := calls(); n != 1 {
			t.Errorf("the first write offline made %d ssh call(s), want 1 (no retry after a timeout):\n%s", n, r.Out())
		}
		for _, args := range [][]string{
			{"memory", "add", "--body", "second note on the train"},
			{"memory", "list"},
			{"context", "--for-file", "README.md"},
			{"memory", "add", "--live", "--body", "a live note on the train"},
		} {
			r := lap.LoreIn(repo, args...).OK(t).Never(t, "lore live: offline")
			if n := calls(); n != 0 {
				t.Errorf("lore %s right after a failed fetch made %d ssh call(s), want none:\n%s", strings.Join(args, " "), n, r.Out())
			}
		}
		lap.LoreIn(repo, "sync")
		if n := calls(); n == 0 {
			t.Error("lore sync made no ssh call: it must always try the remote")
		}
	})
}
