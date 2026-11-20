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
// when that is over 70 % of the step's -timeout. A test with no row counts
// as its suite's mean, so a new test is in the sum the day it is added;
// re-measure (test-times.sh) when many are new. The fix for a failure is
// to split the part (another test:<suite>:<part> task with its own -run,
// its GATE_STEPS line, and its pattern in the catch-all's -skip) or to
// raise its PARALLEL when its tests allow it, never to raise the timeout
// alone. The limit is before the machine factor (tests/lib/testtime.sh),
// which every part keeps.

import (
	"bufio"
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
// at most sel.parallel at a time (a perfect packing, but never shorter
// than the longest one). tests are the suite's test names.
func estimate(sel gateSelector, tests []string, times map[string]testTime) stepEstimate {
	var mean float64
	for _, tt := range times {
		mean += tt.secs
	}
	if len(times) > 0 {
		mean /= float64(len(times))
	}
	par := sel.parallel
	if par <= 0 {
		par = 1 // no -parallel given: count it at its slowest
	}
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
	secs := serial + max(parMax, parSum/float64(par))
	e.wall = time.Duration(secs * float64(time.Second)).Round(time.Second)
	slices.SortFunc(run, func(a, b named) int { return cmp.Compare(b.secs, a.secs) })
	for _, r := range run[:min(5, len(run))] {
		e.longest = append(e.longest, fmt.Sprintf("%s %.0fs", r.name, r.secs))
	}
	return e
}

// overBudget lists every step on a measured suite whose estimate is over
// budgetShare of its timeout, or that has no -timeout to hold it to.
func overBudget(sels []gateSelector, suiteTests map[string][]string, times map[string]map[string]testTime) []string {
	var out []string
	for _, sel := range sels {
		tt, ok := times[sel.dir]
		if !ok {
			continue
		}
		e := estimate(sel, suiteTests[sel.dir], tt)
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
		out = append(out, fmt.Sprintf("%s: %d tests of %s (%d not measured, counted at the suite's mean) need about %s at -parallel %d, over %.0f%% of its %s -timeout (%s); longest: %s",
			e.step, e.tests, sel.dir, e.unmeasured, e.wall, sel.parallel, budgetShare*100, e.timeout, limit.Round(time.Second), strings.Join(e.longest, ", ")))
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
			e := estimate(sel, suiteTests[sel.dir], tt)
			t.Logf("%-18s %3d tests  about %-6s of %s at -parallel %d", e.step, e.tests, e.wall, e.timeout, sel.parallel)
		}
	}
	for _, msg := range overBudget(sels, suiteTests, times) {
		t.Errorf("%s. Split the part (a test:<suite>:<part> task with its own -run, a GATE_STEPS line, its pattern in the catch-all's -skip) or raise its PARALLEL; never the timeout alone (devco D-779)", msg)
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
	got := overBudget(sels, suite, all)
	if len(got) != 1 || !strings.Contains(got[0], "journey-rest") || !strings.Contains(got[0], "1 not measured") {
		t.Fatalf("an over-full part must be flagged naming the unmeasured test: %v", got)
	}
	times["TestJourneyNew"] = testTime{secs: 1, parallel: true}
	if got := overBudget(sels, suite, all); len(got) != 0 {
		t.Fatalf("(20 x 61 s + 1 s) / 2 = 611 s fits 630 s: %v", got)
	}
	times["TestJourneyNew"] = testTime{secs: 120, parallel: false}
	if got := overBudget(sels, suite, all); len(got) != 1 {
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
	if got := overBudget(gateSelectors(t, split), suite, all); len(got) != 0 {
		t.Fatalf("the same tests over two parts fit: %v", got)
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
