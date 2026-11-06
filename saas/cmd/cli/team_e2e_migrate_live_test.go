//go:build teame2e

package main

// FORMAT_EVOLUTION 7.8 TestMigrateRaceLiveWritesAndClaims: three agent
// clones claim, renew, release, add notes and start tasks in a loop while
// alice's `lore migrate run` is paused right after its live step (the
// test build's "live" point, team_e2e_migrate_hooks_test.go pauseAt).

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	th "saas/cmd/cli/internal/teamharness"
)

// liveLoopCaps is what the loop's commands need beyond the crew floor.
var liveLoopCaps = []th.Cap{th.Cmd("task", "heartbeat"), th.Cmd("task", "release"), th.Cmd("task", "note"), th.Cmd("task", "start"), th.Cmd("migrate", "run")}

// livePausedRounds is how many loop rounds each agent runs while the
// migration is paused at "live" (and again after it finished).
const livePausedRounds = 4

// liveWrite is one write a looping agent made that reported success.
type liveWrite struct {
	agent string
	op    string // claim | heartbeat | release | note | start
	task  string
	note  string // the note text (op note)
}

// liveLooper is one agent's clone, its own claim pool and its record.
type liveLooper struct {
	name  string
	c     *th.Clone
	pool  []string        // the task only this agent claims (max_claims_per_agent 1; final claim state is its own)
	held  map[string]bool // claim state after its last successful claim/release
	wrote []liveWrite
	rnd   *rand.ChaCha8 // fixed seed per looper: a failure replays
}

// round runs one random write on the looper's pool or the shared task and
// records it when it reported success. Contended, lost or fenced writes
// are fine (another agent's race), anything else fails the test.
func (l *liveLooper) round(t *testing.T, shared string, i int) {
	t.Helper()
	task := l.pool[0] // one task per agent (max_claims_per_agent 1)
	var w liveWrite
	var r th.Result
	switch l.rnd.Uint64() >> 62 { // the top two bits pick one of four writes
	case 0: // claim or release, whichever flips the state
		if l.held[task] {
			w = liveWrite{agent: l.name, op: "release", task: task}
			r = l.c.Lore("task", "release", task)
		} else {
			w = liveWrite{agent: l.name, op: "claim", task: task}
			r = l.c.Lore("task", "claim", task)
		}
	case 1:
		if !l.held[task] {
			return
		}
		w = liveWrite{agent: l.name, op: "heartbeat", task: task}
		r = l.c.Lore("task", "heartbeat", task)
	case 2:
		text := fmt.Sprintf("%s note %d", l.name, i)
		w = liveWrite{agent: l.name, op: "note", task: shared, note: text}
		r = l.c.Lore("task", "note", shared, text)
	default:
		if !l.held[task] {
			return
		}
		w = liveWrite{agent: l.name, op: "start", task: task}
		r = l.c.Lore("task", "start", task)
	}
	switch r.Code {
	case 0:
		l.wrote = append(l.wrote, w)
		switch w.op {
		case "claim":
			l.held[task] = true
		case "release":
			l.held[task] = false
		}
	case th.ExitContended, th.ExitLostClaim:
		// a teammate's write won this round; nothing reported success
	default:
		t.Errorf("%s: %s %s during the migration failed:\n%s", l.name, w.op, w.task, r)
	}
}

// TestMigrateRaceLiveWritesAndClaims (7.5, 7.8): northwind/tools holds v1
// memory rows and live tasks; w-long holds a claim on a long task from
// before the migration and renews it throughout. Three agents loop claim / heartbeat / release / note /
// start while alice's pushing `lore migrate run` is paused at "live" and
// after it finished. Afterwards: every write that reported success is
// there (each note on the shared task, each looper's final claim state on
// its pool, started tasks in_progress), the long claim's token and epoch
// are unchanged, the v1 rows are `_v: 2`, and the run either succeeded or
// stopped busy/stale and a rerun completed.
//
// The migrating clone is not one of the loopers: the old binary's live
// session holds L for the whole command (BUG-LIVE-LOCK-WHOLE-COMMAND), so
// a looper on alice's own clone would wait on her paused run.
func TestMigrateRaceLiveWritesAndClaims(t *testing.T) {
	cr := newCrew(t, liveLoopCaps...)
	for i := range v1Memories {
		id, doc := v1MemoryDoc(i)
		cr.toolsC.WriteFile(dataDir+"/"+tblMemories+"/"+id+".json", doc)
	}
	cr.toolsC.CommitAll("lore: v0.1.17 rows")
	cr.toolsC.AdminPush("origin", "main")

	shared := cr.task(t, "shared notes task")
	long := cr.task(t, "long claim across the migration")
	var loopers []*liveLooper
	seeds := [...][32]byte{{1, 7}, {2, 7}, {3, 7}} // fixed seeds: a failure replays
	for n, seed := range seeds {
		name := fmt.Sprintf("w-%d", n+1)
		_, c := cr.agent(t, name)
		l := &liveLooper{name: name, c: c, held: map[string]bool{}, rnd: rand.NewChaCha8(seed)}
		l.pool = []string{cr.task(t, name+" pool")}
		loopers = append(loopers, l)
	}
	// The long claim's holder is a fourth agent (max_claims_per_agent 1):
	// it only renews, from the first looper's goroutine.
	_, holder := cr.agent(t, "w-long")
	for _, l := range loopers {
		l.c.MustLore("task", "list")
	}
	holder.MustLore("task", "claim", long).Says(t, "claimed")
	before, ok := cr.claim(t, long)
	if !ok || before.ClaimToken == "" {
		t.Fatalf("w-1's long claim is not on the signals ref: %+v", before)
	}

	// Loops run in the background; a round counter per phase tells the
	// test when each agent has done enough rounds with the run paused.
	var mu sync.Mutex
	paused, finished := false, false
	rounds := make([]int, len(loopers))
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for n, l := range loopers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				l.round(t, shared, i)
				if n == 0 {
					if r := holder.Lore("task", "heartbeat", long); r.Code != 0 { // keep the long claim renewed throughout
						t.Errorf("the long claim's heartbeat during the migration failed:\n%s", r)
					}
				}
				mu.Lock()
				if paused || finished {
					rounds[n]++
				}
				mu.Unlock()
			}
		}()
	}
	waitRounds := func() {
		deadline := time.Now().Add(pauseWait * 3)
		for {
			mu.Lock()
			low := rounds[0]
			for _, r := range rounds {
				low = min(low, r)
			}
			mu.Unlock()
			if low >= livePausedRounds {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the loops did not reach %d rounds (%v)", livePausedRounds, rounds)
			}
			time.Sleep(50 * time.Millisecond) // polling the loop counters
		}
	}

	p := pauseAt(t, cr.toolsC, "live", "migrate", "run")
	mu.Lock()
	paused = true
	mu.Unlock()
	waitRounds()
	res := p.resume()
	mu.Lock()
	paused, finished = false, true
	rounds = make([]int, len(loopers))
	mu.Unlock()
	if res.Code != 0 {
		low := strings.ToLower(res.Out())
		if !strings.Contains(low, "busy") && !strings.Contains(res.Out(), "E_STALE") {
			t.Fatalf("migrate run during live writes failed with neither \"live ref busy\" nor E_STALE:\n%s", res)
		}
		cr.toolsC.MustLore("migrate", "run")
	}
	waitRounds()
	close(stop)
	wg.Wait()
	if t.Failed() {
		return
	}

	assertMigrated(t, "alice", memoryFiles(t, cr.toolsC))
	after, ok := cr.claim(t, long)
	if !ok || after.ClaimToken != before.ClaimToken || after.ClaimEpoch != before.ClaimEpoch || after.Session != before.Session {
		t.Errorf("the long claim changed across the migration:\nbefore %+v\nafter  %+v", before, after)
	}
	for _, l := range loopers {
		l.c.MustLore("task", "list")
		for _, task := range l.pool {
			sig, held := cr.claim(t, task)
			if held != l.held[task] || (held && sig.Session != l.c.A.Env(th.EnvLoreSession)) {
				t.Errorf("%s's last successful claim/release left %s held=%v, the signals ref has held=%v (%+v)", l.name, task, l.held[task], held, sig)
			}
		}
	}
	show := cr.toolsC.MustLore("task", "show", shared).Out()
	started := map[string]bool{}
	writes := 0
	for _, l := range loopers {
		for _, w := range l.wrote {
			writes++
			switch w.op {
			case "note":
				if !strings.Contains(show, w.note) {
					t.Errorf("note %q reported success and is gone:\n%s", w.note, show)
				}
			case "start":
				started[w.task] = true
			}
		}
	}
	for task := range started {
		if got := subtaskStatus(t, cr.toolsC, task); got != "in_progress" {
			t.Errorf("%s was started (reported success) and is %q", task, got)
		}
	}
	for _, id := range append([]string{shared, long}, loopers[0].pool...) {
		r := cr.liveOrMainTask(t, id)
		if v := strings.TrimSpace(string(r.Raw["_v"])); v != "2" {
			t.Errorf("live row %s is at _v %s, want 2", id, v)
		}
	}
	t.Logf("%d successful loop writes; migrate run: exit %d", writes, res.Code)
}
