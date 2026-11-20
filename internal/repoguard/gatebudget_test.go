package repoguard

// gatebudget_test.go — devco D-779: every gate part of a measured suite
// finishes well inside its go test -timeout. The journey catch-all
// (journey-rest, about 210 tests at -parallel 2) and the stories catch-all
// grew past their 15 minutes one test at a time; go test then killed the
// step with about 85 journeys never started, and no gate had run them for
// a day before anyone saw it. Raising a limit only hides the next growth,
// so the guard works on measured times instead.
//
// tests/lib/gate-test-times.tsv holds each test's measured wall seconds
// and whether it runs in parallel (tests/lib/test-times.sh writes it from
// a go test -json run). For every GATE_STEPS step that runs go test on a
// measured suite, the guard estimates the step's wall time the way go test
// schedules it (the serial tests one after another, then the parallel ones
// -parallel at a time, never shorter than the longest of them) and fails
// when that is over 70 % of the step's -timeout. The parallel counted is
// the one the step really gets: its -parallel, but never more than the
// cores its GATE_STEPS line asks of the load budget. The stories catch-all
// ran at -parallel 8 on a 4-core share; counted at 8 it looked like 6m38s
// (44 %), and it took 866 s of its 900 in the gate (devco D-784): on a
// full gate the other steps hold every other core, so tests past the
// step's share wait for a core instead of running beside each other.
// Counted that way it was 9m44s, still under 70 %: the table's times come
// from a quieter machine than a full gate, so each suite's count is also
// multiplied by its gate slowdown (gateSlowdown), how much slower than
// counted its parts really ran in a full gate. A test with no row counts
// as its suite's mean, so a new test is in the sum the day it is added;
// re-measure (test-times.sh) when many are new. The fix for a failure is
// to split the part (another test:<suite>:<part> task with its own -run,
// its GATE_STEPS line, and its pattern in the catch-all's -skip) or to
// raise its PARALLEL and the cores of its GATE_STEPS line together when
// its tests allow it, never to raise the timeout alone. The limit is before the machine factor (tests/lib/testtime.sh),
// which every part keeps.

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// budgetShare is the part of a step's -timeout its measured tests may
// fill: the rest is room for a slower day (the gate's load swings from 60
// to over 150 on the gate server, and every step slows with it).
const budgetShare = 0.70

// gateSlowdown is, per measured suite, how much slower than the guard's
// count its gate parts really ran in a full gate: the step's wall in the
// gate's timings (tmp/check/timings.tsv, or the ✓ lines of its log) over
// the step's estimate (go test -v -run TestGateStepsFitTheirTimeout ./internal/repoguard/
// prints it), the worst part of the suite, rounded up. A suite not listed
// counts at 1. Raise it when a gate shows a part slower than counted;
// lower it only after re-measuring the suite (tests/lib/test-times.sh).
// Measured in the gate of dev 64ce92d8 (devco D-784): the stories
// catch-all took 866 s against 584 s counted (1.48); the journey parts
// took 0.6 to 0.8 of their count (journey-rest-ah 255 s of 433 s,
// journey-rest-in 239 s of 381 s, journey-teamlead 226 s of 276 s), so
// they stay at 1.
var gateSlowdown = map[string]float64{"tests/stories": 1.5}

// measuredSuites must have rows in the table: the two suites whose gate
// parts outgrew their limit (D-779). A suite with rows is checked whether
// listed here or not.
var measuredSuites = []string{"tests/journeys", "tests/stories"}

// gateTestTimes is the measured table, from the repo root.
var gateTestTimes = filepath.Join(repoRoot, "tests", "lib", "gate-test-times.tsv")

// testTime is one measured top-level test.
type testTime struct {
	secs     float64
	parallel bool
}

// readTestTimes parses the table: suite folder → test name → time.
func readTestTimes(t *testing.T, text string) map[string]map[string]testTime {
	t.Helper()
	out := map[string]map[string]testTime{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 || (f[3] != "parallel" && f[3] != "serial") {
			t.Fatalf("%s:%d: want <suite> <test> <seconds> parallel|serial, tab-separated: %q", gateTestTimes, n, line)
		}
		secs, err := strconv.ParseFloat(f[2], 64)
		if err != nil || secs < 0 {
			t.Fatalf("%s:%d: bad seconds %q", gateTestTimes, n, f[2])
		}
		if out[f[0]] == nil {
			out[f[0]] = map[string]testTime{}
		}
		out[f[0]][f[1]] = testTime{secs: secs, parallel: f[3] == "parallel"}
	}
	return out
}

// stepEstimate is a gate step's estimated wall time on a measured suite.
type stepEstimate struct {
	step       string
	wall       time.Duration
	timeout    time.Duration
	tests      int
	unmeasured int
	longest    []string // the five longest tests, "Name 123s"
}

// estimate adds up the tests sel runs, as go test schedules them: every
// serial top-level test first, one after another, then the parallel ones
// at most effectiveParallel(sel) at a time (a perfect packing, but never
// shorter than the longest one), times slow, the suite's gate slowdown (1
// when it has none). tests are the suite's test names.
func estimate(sel gateSelector, tests []string, times map[string]testTime, slow float64) stepEstimate {
	var mean float64
	for _, tt := range times {
		mean += tt.secs
	}
	if len(times) > 0 {
		mean /= float64(len(times))
	}
	par := effectiveParallel(sel)
	e := stepEstimate{step: sel.step, timeout: sel.timeout}
	var serial, parSum, parMax float64
	type named struct {
		name string
		secs float64
	}
	var run []named
	for _, name := range tests {
		if !sel.selects(name) {
			continue
		}
		e.tests++
		tt, ok := times[name]
		if !ok {
			e.unmeasured++
			tt = testTime{secs: mean, parallel: true}
		}
		run = append(run, named{name, tt.secs})
		if tt.parallel {
			parSum += tt.secs
			parMax = max(parMax, tt.secs)
		} else {
			serial += tt.secs
		}
	}
	secs := (serial + max(parMax, parSum/float64(par))) * max(slow, 1)
	e.wall = time.Duration(secs * float64(time.Second)).Round(time.Second)
	slices.SortFunc(run, func(a, b named) int { return cmp.Compare(b.secs, a.secs) })
	for _, r := range run[:min(5, len(run))] {
		e.longest = append(e.longest, fmt.Sprintf("%s %.0fs", r.name, r.secs))
	}
	return e
}

// effectiveParallel is how many of sel's tests run side by side on a full
// gate: its -parallel, capped at the cores its GATE_STEPS line asks for
// (devco D-784), and 1 when it gives no -parallel (counted at its slowest).
func effectiveParallel(sel gateSelector) int {
	par := sel.parallel
	if sel.cores > 0 && sel.cores < par {
		par = sel.cores
	}
	return max(par, 1)
}

// overBudget lists every step on a measured suite whose estimate is over
// budgetShare of its timeout, or that has no -timeout to hold it to.
func overBudget(sels []gateSelector, suiteTests map[string][]string, times map[string]map[string]testTime, slow map[string]float64) []string {
	var out []string
	for _, sel := range sels {
		tt, ok := times[sel.dir]
		if !ok {
			continue
		}
		e := estimate(sel, suiteTests[sel.dir], tt, slow[sel.dir])
		if e.tests == 0 {
			continue
		}
		if e.timeout <= 0 {
			out = append(out, fmt.Sprintf("%s runs %d tests of %s with no -timeout: give it one (through tests/lib/testtime.sh)", e.step, e.tests, sel.dir))
			continue
		}
		limit := time.Duration(float64(e.timeout) * budgetShare)
		if e.wall <= limit {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %d tests of %s (%d not measured, counted at the suite's mean) need about %s at -parallel %d (%d cores, gate slowdown %.2g), over %.0f%% of its %s -timeout (%s); longest: %s",
			e.step, e.tests, sel.dir, e.unmeasured, e.wall, sel.parallel, sel.cores, max(slow[sel.dir], 1), budgetShare*100, e.timeout, limit.Round(time.Second), strings.Join(e.longest, ", ")))
	}
	return out
}

// unmeasuredSteps lists every step on a measured suite where more than
// half of the tests it runs have no row (a part made of a single
// unmeasured test included). The suite mean stands in for one new test,
// not for a whole part: the persona parts (journey-teamlead and the
// rest) were never measured, so the guard counted journey-teamlead's 15
// tests at the mean, about 1m53s, while it really took 367 s (devco
// D-783). Such a step is measured with its own go test -json run before
// its estimate means anything.
func unmeasuredSteps(sels []gateSelector, suiteTests map[string][]string, times map[string]map[string]testTime) []string {
	var out []string
	for _, sel := range sels {
		tt, ok := times[sel.dir]
		if !ok {
			continue
		}
		e := estimate(sel, suiteTests[sel.dir], tt, 1)
		if e.tests == 0 || e.unmeasured*2 <= e.tests {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %d of its %d tests of %s have no measured time, so its estimate (%s) is the suite's mean, not the part: measure it (go test -json with the part's -run, then tests/lib/test-times.sh %s <file>)",
			e.step, e.unmeasured, e.tests, sel.dir, e.wall, sel.dir))
	}
	return out
}

// TestGateStepsFitTheirTimeout is the D-779 guard on the real Taskfile
// and the measured table.
func TestGateStepsFitTheirTimeout(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(loreTaskfile)
	if err != nil {
		t.Fatal(err)
	}
	table, err := os.ReadFile(gateTestTimes)
	if err != nil {
		t.Fatalf("%v: the gate's measured test times are missing (tests/lib/test-times.sh writes them)", err)
	}
	times := readTestTimes(t, string(table))
	suiteTests := map[string][]string{}
	for _, tc := range taggedSuiteTests(t) {
		suiteTests[tc.dir] = append(suiteTests[tc.dir], tc.name)
	}
	for _, dir := range measuredSuites {
		if len(times[dir]) < len(suiteTests[dir])/2 {
			t.Errorf("%s has %d measured tests of %d: re-measure it (go test -json, then tests/lib/test-times.sh %s <file>)", dir, len(times[dir]), len(suiteTests[dir]), dir)
		}
	}
	sels := gateSelectors(t, raw)
	for _, dir := range measuredSuites {
		if !slices.ContainsFunc(sels, func(s gateSelector) bool { return s.dir == dir && s.timeout > 0 && s.parallel > 0 }) {
			t.Errorf("no gate step on %s was read with its -parallel and -timeout: the Taskfile parser no longer reads them", dir)
		}
	}
	// go test -v shows every measured step's estimate, the numbers to
	// read when re-balancing the parts.
	for _, sel := range sels {
		if tt, ok := times[sel.dir]; ok {
			e := estimate(sel, suiteTests[sel.dir], tt, gateSlowdown[sel.dir])
			t.Logf("%-18s %3d tests  about %-6s of %s at -parallel %d on %d cores", e.step, e.tests, e.wall, e.timeout, sel.parallel, sel.cores)
		}
	}
	for _, msg := range unmeasuredSteps(sels, suiteTests, times) {
		t.Errorf("%s (devco D-783)", msg)
	}
	for _, msg := range overBudget(sels, suiteTests, times, gateSlowdown) {
		t.Errorf("%s. Split the part (a test:<suite>:<part> task with its own -run, a GATE_STEPS line, its pattern in the catch-all's -skip) or raise its PARALLEL with its cores; never the timeout alone (devco D-779)", msg)
	}
}

// TestGateBudgetBites: a catch-all part at -parallel 2 whose tests fill
// more than 70 % of its 15 minutes is flagged, a test with no row counts at
// the suite's mean, and the same tests split over two parts pass.
func TestGateBudgetBites(t *testing.T) {
	t.Parallel()
	var tests []string
	times := map[string]testTime{}
	for i := range 20 {
		name := fmt.Sprintf("TestJourneyCase%02d", i)
		tests = append(tests, name)
		times[name] = testTime{secs: 61, parallel: true}
	}
	tests = append(tests, "TestJourneyNew") // no row: counts as the mean, 61 s
	all := map[string]map[string]testTime{"tests/journeys": times}
	suite := map[string][]string{"tests/journeys": tests}

	one := []byte(`vars:
  GATE_STEPS: |
    journey-rest  2  test:journeys:rest PARALLEL=2
tasks:
  test:journeys:part:
    cmds:
      - "go test -tags journeys -count=1 -parallel {{.PARALLEL | default 16}} -timeout $({{.TEST_TIME}} 15m) {{.FILTER}} ./tests/journeys/"
  test:journeys:rest:
    cmds:
      - task: test:journeys:part
        vars: {PART: rest, FILTER: "-skip '^TestFrame'"}
`)
	sels := gateSelectors(t, one)
	if len(sels) != 1 || sels[0].parallel != 2 || sels[0].timeout != 15*time.Minute {
		t.Fatalf("the step line's PARALLEL and the part's -timeout must be read: %+v", sels)
	}
	// 21 tests x 61 s at -parallel 2 = 640 s, over 70 % of 900 s (630 s);
	// the 20 measured ones alone (610 s) fit, so the unmeasured test tips it.
	got := overBudget(sels, suite, all, nil)
	if len(got) != 1 || !strings.Contains(got[0], "journey-rest") || !strings.Contains(got[0], "1 not measured") {
		t.Fatalf("an over-full part must be flagged naming the unmeasured test: %v", got)
	}
	times["TestJourneyNew"] = testTime{secs: 1, parallel: true}
	if got := overBudget(sels, suite, all, nil); len(got) != 0 {
		t.Fatalf("(20 x 61 s + 1 s) / 2 = 611 s fits 630 s: %v", got)
	}
	times["TestJourneyNew"] = testTime{secs: 120, parallel: false}
	if got := overBudget(sels, suite, all, nil); len(got) != 1 {
		t.Fatalf("a serial test adds its whole time (120 + 610 s): %v", got)
	}

	split := []byte(`vars:
  GATE_STEPS: |
    journey-rest-a  2  test:journeys:rest-a PARALLEL=2
    journey-rest    2  test:journeys:rest PARALLEL=2
tasks:
  test:journeys:part:
    cmds:
      - "go test -tags journeys -count=1 -parallel {{.PARALLEL | default 16}} -timeout $({{.TEST_TIME}} 15m) {{.FILTER}} ./tests/journeys/"
  test:journeys:rest-a:
    cmds:
      - task: test:journeys:part
        vars: {PART: rest-a, FILTER: "-run '^TestJourneyCase0'"}
  test:journeys:rest:
    cmds:
      - task: test:journeys:part
        vars: {PART: rest, FILTER: "-skip '^Test(Frame|JourneyCase0)'"}
`)
	if got := overBudget(gateSelectors(t, split), suite, all, nil); len(got) != 0 {
		t.Fatalf("the same tests over two parts fit: %v", got)
	}

	// A part whose tests mostly have no row is flagged as unmeasured,
	// even when the suite mean makes it look small (devco D-783: the
	// persona parts counted at the mean); one new test in a measured part
	// is not.
	persona := []byte(`vars:
  GATE_STEPS: |
    journey-teamlead  3  test:journeys:teamlead PARALLEL=2
    journey-rest      3  test:journeys:rest PARALLEL=3
tasks:
  test:journeys:part:
    cmds:
      - "go test -tags journeys -count=1 -parallel {{.PARALLEL | default 16}} -timeout $({{.TEST_TIME}} 15m) {{.FILTER}} ./tests/journeys/"
  test:journeys:teamlead:
    cmds:
      - task: test:journeys:part
        vars: {PART: teamlead, FILTER: "-run '^TestJourneyTeamLead'"}
  test:journeys:rest:
    cmds:
      - task: test:journeys:part
        vars: {PART: rest, FILTER: "-skip '^Test(Frame|JourneyTeamLead)'"}
`)
	personaTests := append(slices.Clone(tests), "TestJourneyTeamLeadA", "TestJourneyTeamLeadB", "TestJourneyTeamLeadC")
	personaSuite := map[string][]string{"tests/journeys": personaTests}
	got = unmeasuredSteps(gateSelectors(t, persona), personaSuite, all)
	if len(got) != 1 || !strings.HasPrefix(got[0], "journey-teamlead: 3 of its 3 tests") {
		t.Fatalf("a part with no measured test must be flagged, the catch-all with one new test not: %v", got)
	}
	for _, n := range []string{"TestJourneyTeamLeadA", "TestJourneyTeamLeadB"} {
		times[n] = testTime{secs: 120, parallel: false}
	}
	if got := unmeasuredSteps(gateSelectors(t, persona), personaSuite, all); len(got) != 0 {
		t.Fatalf("2 of 3 measured is enough: %v", got)
	}

	// A step counts at the cores of its GATE_STEPS line when they are
	// fewer than its -parallel (devco D-784): the stories catch-all at
	// -parallel 8 on 4 cores. 20 tests x 61 s fit at -parallel 2 on 2
	// cores (610 s) but not at -parallel 8 on 1 core (1220 s), which the
	// old count (-parallel only) let through at 153 s.
	times["TestJourneyNew"] = testTime{secs: 1, parallel: true}
	narrow := bytes.Replace(one, []byte("journey-rest  2  test:journeys:rest PARALLEL=2"), []byte("journey-rest  1  test:journeys:rest PARALLEL=8"), 1)
	if sels := gateSelectors(t, narrow); len(sels) != 1 || sels[0].cores != 1 || sels[0].parallel != 8 {
		t.Fatalf("the step line's cores and PARALLEL must be read: %+v", sels)
	}
	if got := overBudget(gateSelectors(t, narrow), suite, all, nil); len(got) != 1 || !strings.Contains(got[0], "-parallel 8 (1 cores,") {
		t.Fatalf("a part with -parallel 8 on 1 core runs its tests one at a time and must be flagged: %v", got)
	}
	if got := overBudget(gateSelectors(t, one), suite, all, nil); len(got) != 0 {
		t.Fatalf("-parallel 2 on 2 cores (611 s) fits 630 s: %v", got)
	}
	// The suite's gate slowdown multiplies the count: the same 611 s at
	// 1.5 (a suite whose parts ran half again slower in a full gate than
	// counted, as the stories did in devco D-784) is 917 s, over 630 s.
	slow := map[string]float64{"tests/journeys": 1.5}
	if got := overBudget(gateSelectors(t, one), suite, all, slow); len(got) != 1 || !strings.Contains(got[0], "gate slowdown 1.5") {
		t.Fatalf("a suite's gate slowdown must count: %v", got)
	}
	if got := overBudget(gateSelectors(t, split), suite, all, slow); len(got) != 0 {
		t.Fatalf("split over two parts, each part fits even at 1.5: %v", got)
	}

	// The default of {{.PARALLEL | default N}} stands in when the step
	// sets none, and a part that passes PARALLEL on keeps the step's.
	dflt := []byte(`vars:
  GATE_STEPS: |
    stories-ww  3  test:stories:ww PARALLEL=6
    stories     4  test:stories:rest
tasks:
  test:stories:part:
    cmds:
      - "go test -tags 'stories journeys' -count=1 -parallel {{.PARALLEL | default 8}} -timeout $({{.TEST_TIME}} 15m) {{.FILTER}} ./tests/stories/"
  test:stories:ww:
    cmds:
      - task: test:stories:part
        vars: {PART: ww, FILTER: "-run '^TestStoryWW'", PARALLEL: "{{.PARALLEL}}"}
  test:stories:rest:
    cmds:
      - task: test:stories:part
        vars: {PART: rest, FILTER: "-skip '^TestStoryWW'", PARALLEL: "{{.PARALLEL}}"}
`)
	sels = gateSelectors(t, dflt)
	if len(sels) != 2 || sels[0].parallel != 6 || sels[1].parallel != 8 {
		t.Fatalf("want -parallel 6 from the step line and 8 from the default: %+v", sels)
	}
}
