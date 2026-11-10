package setup

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// Save & next reuses the step's fresh check results (owner 2026-11-07):
// right after a check of the same answers nothing is probed again; a
// failed check, changed answers or results older than the TTL run again;
// a check that warned runs again after the write.
func TestApplyReusesFreshChecks(t *testing.T) {
	env, e := started(t)
	clock := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return clock }
	check := func(a onboard.Answers) {
		t.Helper()
		if _, err := e.Check(bg, CheckIn{Setup: "personal", Step: "identity", Answers: a}); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(a onboard.Answers) int {
		t.Helper()
		before := env.prober.calls
		if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: a}); err != nil {
			t.Fatal(err)
		}
		return env.prober.calls - before
	}

	check(emailAnswers)
	clock = clock.Add(12 * time.Second)
	if n := apply(emailAnswers); n != 0 {
		t.Errorf("fresh ok results: save probed %d time(s), want 0", n)
	}

	// Stale: past the TTL every check runs again (gate; all ok after).
	check(emailAnswers)
	clock = clock.Add(onboard.CheckFreshTTL)
	if n := apply(emailAnswers); n != 1 {
		t.Errorf("stale results: save probed %d time(s), want 1", n)
	}

	// Changed answers: the check of other answers does not count.
	check(emailAnswers)
	if n := apply(onboard.Answers{"email": text("other@agency.example")}); n != 1 {
		t.Errorf("changed answers: save probed %d time(s), want 1", n)
	}

	// A warning is not a failure: the gate keeps it, the write's re-check
	// runs it again (only it).
	env.prober.results[onboard.CheckIdentityGitEmail] = ProbeResult{Status: onboard.StatusWarn}
	check(emailAnswers)
	env.prober.results[onboard.CheckIdentityGitEmail] = ProbeResult{Status: onboard.StatusOK}
	if n := apply(emailAnswers); n != 1 {
		t.Errorf("warned check: save probed %d time(s), want 1 (after the write)", n)
	}
}
