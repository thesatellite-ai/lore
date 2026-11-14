//go:build journeys && !windows

package journeys

// agentsprint_test.go — many coding-agent sessions on one mission (G-10,
// G-11). Kim gives a mission of tasks to six agent sessions, three in the
// laptop's clone and three in the desk's, and they all loop `task next
// --push` → `task done` → `lore sync` at once. Every task is finished
// exactly once: a task another session finished (in this clone or pushed
// from the other) is never handed out again, even while this clone's row
// still says todo, and a second done is a no-op naming who finished it.
// done_by never holds conflict markers and every sync passes.

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

// sprintTasks is the size of the mission's queue.
const sprintTasks = 12

var sprintSession = regexp.MustCompile(`LORE_SESSION=(\S+)`)

func TestJourneyAgentSprint(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kim := w.Persona("Kim Sato", "kim@sprint.example")
	lap, desk := kim.Laptop(), kim.NewLaptop("desk")
	remote := w.Remote("github.com/sprint-co/app")
	var clones []*teamharness.Clone
	var mission string

	w.Step("day1/a-mission-for-the-agents", func() {
		dir := filepath.Join(w.Root, "work", lap.Name, "app")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		c := lap.OpenClone(remote, dir)
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
		mission = regexp.MustCompile(`msn_[0-9a-f]+`).FindString(lap.LoreIn(c, "mission", "add", "--title", "sprint", "--body", "agents").OK(t).Out())
		if mission == "" {
			t.Fatal("mission add printed no id")
		}
		for i := 1; i <= sprintTasks; i++ {
			lap.LoreIn(c, "task", "add", "--title", fmt.Sprintf("sprint item %d", i), "--mission", mission, "--commitment", "accepted").OK(t)
		}
		lap.LoreIn(c, "sync").OK(t)
		d := desk.Clone(remote, "app")
		desk.LoreIn(d, "sync").OK(t)
		desk.LoreIn(d, "task", "list", "--mission", mission).OK(t).Says(t, "sprint item 12")
		clones = []*teamharness.Clone{c, d}
	})

	w.Step("day1/six-agents-work-the-queue-at-once", func() {
		type agent struct {
			label string
			c     *teamharness.Clone
		}
		var agents []agent
		for i := 0; i < 6; i++ {
			c := clones[i%2]
			label := fmt.Sprintf("bot%d", i+1)
			r := c.Lore("agent", "start", "--label", label).OK(t)
			m := sprintSession.FindStringSubmatch(r.Out())
			if m == nil {
				t.Fatalf("agent start printed no session: %s", r)
			}
			agents = append(agents, agent{label, c.With("LORE_HUMAN=", "LORE_ACTOR=agent:"+label, "LORE_SESSION="+m[1])})
		}
		var mu sync.Mutex
		finished := map[string][]string{} // task → who finished it
		var failures []string
		var wg sync.WaitGroup
		for _, a := range agents {
			wg.Add(1)
			go func(a agent) {
				defer wg.Done()
				for round := 1; round <= sprintTasks; round++ {
					r := a.c.Lore("task", "next", "--mission", mission, "--push")
					if r.Code == teamharness.ExitNoWork {
						return
					}
					id := lpTaskID.FindString(r.Out())
					if r.Code != 0 || id == "" {
						mu.Lock()
						failures = append(failures, a.label+" task next: "+r.Out())
						mu.Unlock()
						continue
					}
					d := a.c.Lore("task", "done", "--id", id, "--no-lesson")
					mu.Lock()
					switch {
					case d.Code != 0:
						failures = append(failures, a.label+" task done "+id+": "+d.Out())
					case !strings.Contains(d.Out(), "already done"):
						finished[id] = append(finished[id], a.label)
					}
					mu.Unlock()
					if round%2 == 0 {
						if s := a.c.Lore("sync"); s.Code != 0 {
							mu.Lock()
							failures = append(failures, a.label+" sync: "+s.Out())
							mu.Unlock()
						}
					}
				}
			}(a)
		}
		wg.Wait()
		for _, f := range failures {
			t.Error(f)
		}
		if len(finished) != sprintTasks {
			t.Errorf("%d of %d tasks finished: %v", len(finished), sprintTasks, finished)
		}
		for id, by := range finished {
			if len(by) != 1 {
				t.Errorf("%s was finished %d times (%v); a task is finished once", id, len(by), by)
			}
		}
	})

	w.Step("day1/everyone-syncs-clean", func() {
		for _, c := range clones {
			c.Lore("sync").OK(t)
		}
		for _, c := range clones {
			c.Lore("sync").OK(t)
			out := c.Lore("task", "list", "--mission", mission, "--status", "done", "--json").OK(t).Out()
			if strings.Contains(out, "<<<<<<<") {
				t.Errorf("a task row holds conflict markers after the sprint: %s", out)
			}
			if n := len(lpTaskID.FindAllString(out, -1)); n < sprintTasks {
				t.Errorf("%s lists %d done tasks, want %d", c.Dir, n, sprintTasks)
			}
		}
	})
}
