//go:build journeys && !windows

package journeys

// claimburst_test.go — twelve coding-agent sessions, each in its own clone,
// claim from one queue at the same instant on a slow remote (devco:D-529). Every push waits in
// the remote's pre-receive hook, so most claims lose the race to the
// signals ref again and again; a small fixed retry count used to give up
// with E_CONTENDED, and a session whose whole view of the ready queue was
// taken by others said E_CONTENDED ("ready work exists") instead of
// E_NO_WORK. Now twelve agents get twelve distinct tasks, and the four
// extra agents read the queue again and answer E_NO_WORK, never
// E_CONTENDED.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// burstAgents is how many tasks the queue holds, and how many sessions
// must each get one; burstExtra more sessions find the queue taken.
const (
	burstAgents = 12
	burstExtra  = 4
)

// slowPushHook makes every push to the remote take a moment, like a busy
// host on a loaded machine: pushes that start together all pass the hook,
// one moves the signals ref and the rest lost the race. It counts pushes.
const slowPushHook = `#!/bin/sh
cat >/dev/null
echo push >>pushes.log
sleep 0.6
exit 0
`

func TestJourneyClaimBurst(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ren := w.Persona("Ren Ito", "ren@burst.example")
	lap := ren.Laptop()
	remote := w.Remote("github.com/burst-co/app")
	var c *teamharness.Clone
	var mission string

	w.Step("day1/a-queue-of-twelve", func() {
		dir := filepath.Join(w.Root, "work", lap.Name, "app")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		c = lap.OpenClone(remote, dir)
		c.Git("init", "-q", "-b", "main")
		c.Git("remote", "add", "origin", remote.URL())
		jWrite(t, filepath.Join(dir, "main.go"), "package main\n")
		c.CommitAll("first")
		c.Git("push", "-q", "-u", "origin", "main")
		lap.LoreIn(c, "init").OK(t)
		lap.LoreIn(c, "actor", "register").OK(t)
		c.CommitAll("lore")
		if r := c.TryGit("push", "-q", "origin", "main"); r.Code != 0 {
			c.Git("push", "-q", "origin", "main") // E_PUSH_AGAIN: the hook added the landing commit
		}
		mission = regexp.MustCompile(`msn_[0-9a-f]+`).FindString(lap.LoreIn(c, "mission", "add", "--title", "burst", "--body", "agents").OK(t).Out())
		if mission == "" {
			t.Fatal("mission add printed no id")
		}
		for i := 1; i <= burstAgents; i++ {
			lap.LoreIn(c, "task", "add", "--title", fmt.Sprintf("burst item %d", i), "--mission", mission, "--commitment", "accepted").OK(t)
		}
		lap.LoreIn(c, "sync").OK(t)
		if err := os.WriteFile(filepath.Join(remote.Dir, "hooks", "pre-receive"), []byte(slowPushHook), 0o755); err != nil { //nolint:gosec // a git hook must be executable
			t.Fatal(err)
		}
	})

	w.Step("day1/sixteen-agents-claim-twelve-tasks-at-once", func() {
		var sessions []*teamharness.Clone
		for i := 1; i <= burstAgents+burstExtra; i++ {
			// One clone per agent: sessions of one clone take turns on its
			// lock, so the race is between clones, as between machines.
			c := lap.Clone(remote, fmt.Sprintf("app%d", i))
			lap.LoreIn(c, "sync").OK(t)
			label := fmt.Sprintf("bot%d", i)
			r := c.Lore("agent", "start", "--label", label).OK(t)
			m := sprintSession.FindStringSubmatch(r.Out())
			if m == nil {
				t.Fatalf("agent start printed no session: %s", r)
			}
			sessions = append(sessions, c.With("LORE_HUMAN=", "LORE_ACTOR=agent:"+label, "LORE_SESSION="+m[1]))
		}
		var mu sync.Mutex
		got := map[string][]string{} // task → who claimed it
		noWork := 0
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, s := range sessions {
			wg.Add(1)
			go func(label string, s *teamharness.Clone) {
				defer wg.Done()
				<-start
				r := s.Lore("task", "next", "--claim", "--mission", mission, "--push")
				id := lpTaskID.FindString(r.Out())
				mu.Lock()
				defer mu.Unlock()
				if r.Code == teamharness.ExitNoWork {
					noWork++
					return
				}
				if r.Code != 0 || id == "" {
					t.Errorf("%s task next --claim: exit %d: %s", label, r.Code, r.Out())
					return
				}
				got[id] = append(got[id], label)
			}(fmt.Sprintf("bot%d", i+1), s)
		}
		close(start)
		wg.Wait()
		if b, err := os.ReadFile(filepath.Join(remote.Dir, "pushes.log")); err == nil {
			t.Logf("the remote took %d pushes", strings.Count(string(b), "push"))
		}
		if len(got) != burstAgents {
			t.Errorf("%d distinct tasks claimed by %d agents, want %d: %v", len(got), burstAgents, burstAgents, got)
		}
		if noWork != burstExtra {
			t.Errorf("%d agents found no work, want the %d extra ones", noWork, burstExtra)
		}
		for id, by := range got {
			if len(by) != 1 {
				t.Errorf("%s was claimed by %d agents (%s); a task goes to one", id, len(by), strings.Join(by, ", "))
			}
		}
	})
}
