//go:build journeys && !windows

package journeys

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Step runs one step of the journey: the person's action, then every
// registered invariant on every awake machine, then every Fact the journey
// has established. name reads "when/what" ("week1/offline-write"); the
// failure messages carry it with the journey day so a failure says where in
// the person's life it broke.
func (w *World) Step(name string, fn func()) {
	w.T.Helper()
	w.step = fmt.Sprintf("day %d %s", w.Day(), name)
	for _, m := range w.machines {
		m.results = nil
	}
	w.T.Logf("── %s", w.step)
	fn()
	w.checkAll()
	w.step = ""
}

// Day is the journey day of the World's clock (day 1 = JourneyStart).
func (w *World) Day() int { return int(w.now.Sub(JourneyStart).Hours()/24) + 1 }

// checkAll runs the shared invariants on every awake machine, then the facts.
func (w *World) checkAll() {
	w.T.Helper()
	// Machines are independent: check them at once (t.Errorf is safe from
	// several goroutines; an invariant never calls Fatal).
	var wg sync.WaitGroup
	for _, m := range w.machines {
		if m.asleep {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, inv := range invariants {
				inv.Check(Ctx{T: w.T, W: w, M: m, Step: w.step + " [" + inv.Name + " on " + m.Name + "]"})
			}
		}()
	}
	wg.Wait()
	for _, f := range w.facts {
		f.Check(Ctx{T: w.T, W: w, Step: w.step + " [fact: " + f.Name + "]"})
	}
}

// Fact is something the journey established that must stay true after every
// later step ("the note written on the old laptop reads the same on the new
// one", "the client repo never holds an internal note"). M is nil in its Ctx.
type Fact struct {
	Name  string
	Check func(c Ctx)
}

// Fact adds a fact, checked once now and after every later step.
func (w *World) Fact(name string, check func(c Ctx)) {
	w.T.Helper()
	f := Fact{Name: name, Check: check}
	w.facts = append(w.facts, f)
	f.Check(Ctx{T: w.T, W: w, Step: w.step + " [fact: " + name + "]"})
}

// envStrict makes Pending steps fail like any other step (the final run).
const envStrict = "JOURNEYS_STRICT"

// Pending runs part of a step whose right behaviour waits on another
// branch's fix (owner names it, e.g. "store-push"). Until JOURNEYS_STRICT=1
// its failures are recorded and logged as PENDING(owner) with every message,
// and the journey goes on; strict, it fails like any step. fn takes
// testing.TB so the recorder can stand in.
func Pending(t *testing.T, owner, what string, fn func(tb testing.TB)) {
	t.Helper()
	if os.Getenv(envStrict) == "1" {
		fn(t)
		return
	}
	rec := &recorder{TB: t}
	done := make(chan struct{})
	go func() { // Fatal ends the goroutine with runtime.Goexit, as in a test
		defer close(done)
		fn(rec)
	}()
	<-done
	if rec.failed {
		t.Logf("PENDING(%s) %s:\n  %s", owner, what, strings.Join(rec.msgs, "\n  "))
	}
}

// recorder is the testing.TB a pending step reports into: failures are
// kept, not reported.
type recorder struct {
	testing.TB
	failed bool
	msgs   []string
}

func (r *recorder) Helper()           {}
func (r *recorder) Fail()             { r.failed = true }
func (r *recorder) Failed() bool      { return r.failed }
func (r *recorder) FailNow()          { r.failed = true; runtime.Goexit() }
func (r *recorder) Error(args ...any) { r.Fail(); r.msgs = append(r.msgs, fmt.Sprint(args...)) }
func (r *recorder) Errorf(f string, args ...any) {
	r.Fail()
	r.msgs = append(r.msgs, fmt.Sprintf(f, args...))
}
func (r *recorder) Fatal(args ...any)            { r.Error(args...); runtime.Goexit() }
func (r *recorder) Fatalf(f string, args ...any) { r.Errorf(f, args...); runtime.Goexit() }
