// Command matrix reads `go test -json` from stdin and prints the team suite's
// pass / fail / pending matrix (LORE_TEAM_SPEC.md §9.3; status files keep the
// same table). A test is pending when it skipped with the harness's PENDING:
// prefix (its commands are not in the binary yet). It exits 1 when any test
// failed, so `task lore:test:team` is red only for real failures, never for
// features that are simply not built.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// pendingMarker must equal teamharness.PendingPrefix; it is repeated here
// because this command only reads test output and must not import the
// test-only harness (which pulls in package testing).
const pendingMarker = "PENDING:"

// State is one test's outcome as the matrix shows it.
type State string

// The closed set of states, worst first (a parent takes its worst child).
const (
	Fail    State = "FAIL"
	Pending State = "pending"
	Pass    State = "pass"
)

// rank orders states so a parent test reports its worst subtest.
func rank(s State) int {
	switch s {
	case Fail:
		return 0
	case Pending:
		return 1
	default:
		return 2
	}
}

// Actions of test2json events this command reacts to beyond per-test ones.
// Since Go 1.24 a test binary that does not compile shows up as
// build-output/build-fail events (keyed by ImportPath, not Package) plus a
// package-level "fail" carrying FailedBuild; without handling them the
// matrix would print "0 pass · 0 FAIL" and look green.
const (
	actionBuildOutput = "build-output"
	actionBuildFail   = "build-fail"
	actionOutput      = "output"
	actionFail        = "fail"
)

// event is the subset of test2json's TestEvent this command reads.
type event struct {
	Action      string `json:"Action"`
	Package     string `json:"Package"`
	Test        string `json:"Test"`
	Output      string `json:"Output"`
	FailedBuild string `json:"FailedBuild"`
}

// Report is everything the matrix prints: per-test states plus the
// run-level failures that have no test to hang on.
type Report struct {
	Tests map[string]result
	// BuildFailed is true when a test binary did not compile; BuildOutput
	// holds the compiler's lines so the reader sees why.
	BuildFailed bool
	BuildOutput []string
	// PackageFailed lists packages go test marked failed although none of
	// their tests failed (setup failure, panic outside a test, timeout);
	// PackageOutput holds their package-level output lines.
	PackageFailed []string
	PackageOutput []string
}

// Failed reports whether the run must be red: a test failed, a build
// failed, or a package failed without a failing test.
func (r Report) Failed() bool {
	if r.BuildFailed || len(r.PackageFailed) > 0 {
		return true
	}
	for _, t := range r.Tests {
		if t.state == Fail {
			return true
		}
	}
	return false
}

// result accumulates one test (top level or subtest).
type result struct {
	state   State
	pending string // the PENDING line, for the "missing" column
	// failure is the first assertion line ("x_test.go:12: …") the test or
	// one of its subtests printed, shown under a FAIL row so the reader sees
	// why without re-running (a flaky failure leaves its evidence).
	failure string
}

// maxFailureLine caps the failure text shown under a FAIL row.
const maxFailureLine = 300

// Collect folds a test2json stream into one State per top-level test (plus
// the first PENDING reason seen for it) and records build and package
// failures that no test carries.
func Collect(r io.Reader) (Report, error) {
	var rep Report
	tests := map[string]*result{}
	pkgOut := map[string][]string{}
	failedPkgs := map[string]bool{}
	testFailedIn := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var ev event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue // non-JSON noise
		}
		if ev.Test == "" {
			switch {
			case ev.Action == actionBuildOutput:
				rep.BuildOutput = append(rep.BuildOutput, strings.TrimRight(ev.Output, "\n"))
			case ev.Action == actionBuildFail:
				rep.BuildFailed = true
			case ev.Action == actionFail && ev.FailedBuild != "":
				rep.BuildFailed = true
			case ev.Action == actionFail && ev.Package != "":
				failedPkgs[ev.Package] = true
			case ev.Action == actionOutput && ev.Package != "":
				pkgOut[ev.Package] = append(pkgOut[ev.Package], strings.TrimRight(ev.Output, "\n"))
			}
			continue
		}
		if ev.Action == actionFail {
			testFailedIn[ev.Package] = true
		}
		res, ok := tests[ev.Test]
		if !ok {
			res = &result{}
			tests[ev.Test] = res
		}
		switch ev.Action {
		case "output":
			if i := strings.Index(ev.Output, pendingMarker); i >= 0 && res.pending == "" {
				res.pending = strings.TrimSpace(ev.Output[i+len(pendingMarker):])
			}
			if res.failure == "" && strings.Contains(ev.Output, "_test.go:") && !strings.Contains(ev.Output, pendingMarker) {
				res.failure = strings.TrimSpace(ev.Output)
			}
		case "pass":
			res.state = Pass
		case "fail":
			res.state = Fail
		case "skip":
			res.state = Pending
			if res.pending == "" {
				res.state = Pass // a skip that is not PENDING is an ordinary skip; never reported as missing work
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Report{}, fmt.Errorf("read test2json: %w", err)
	}
	rep.Tests = fold(tests)
	if !rep.BuildFailed {
		pkgs := make([]string, 0, len(failedPkgs))
		for p := range failedPkgs {
			if !testFailedIn[p] {
				pkgs = append(pkgs, p)
			}
		}
		sort.Strings(pkgs)
		for _, p := range pkgs {
			rep.PackageFailed = append(rep.PackageFailed, p)
			rep.PackageOutput = append(rep.PackageOutput, pkgOut[p]...)
		}
	}
	return rep, nil
}

// fold merges subtests into their top-level test. Any FAIL (the test itself
// or a subtest) is FAIL; otherwise any pending subtest makes the test
// pending (go test reports a parent whose subtests all skipped as "pass",
// which would hide unbuilt work); otherwise the test's own state stands.
func fold(all map[string]*result) map[string]result {
	type acc struct {
		own     State
		subs    []State
		pending string
		failure string
	}
	tops := map[string]*acc{}
	for name, r := range all {
		top, _, isSub := strings.Cut(name, "/")
		a, ok := tops[top]
		if !ok {
			a = &acc{}
			tops[top] = a
		}
		if isSub {
			a.subs = append(a.subs, r.state)
		} else {
			a.own = r.state
		}
		if a.pending == "" {
			a.pending = r.pending
		}
		if a.failure == "" && r.state == Fail {
			a.failure = r.failure
		}
	}
	out := map[string]result{}
	for top, a := range tops {
		st := a.own
		for _, s := range a.subs {
			if rank(s) < rank(st) {
				st = s
			}
		}
		out[top] = result{state: st, pending: a.pending, failure: a.failure}
	}
	return out
}

// group is the spec family a test belongs to, from its name: TestF05_… →
// F05, TestE01_… → E01, TestRecipe12_… → Recipe12, TestSideEffect28_… →
// SideEffect28, others by their first word.
func group(name string) string {
	n := strings.TrimPrefix(name, "Test")
	if i := strings.Index(n, "_"); i > 0 {
		return n[:i]
	}
	return n
}

// Print writes the matrix (or the build failure) to w and returns the exit
// code: 0 green or only pending, 1 any failure, exitWriteFailed when the
// report could not be written. The report is built in memory and written
// once, so one write error check covers every line.
func Print(w io.Writer, rep Report) int {
	var b strings.Builder
	code := buildReport(&b, rep)
	if _, err := io.WriteString(w, b.String()); err != nil {
		return exitWriteFailed
	}
	return code
}

// exitWriteFailed is Print's exit code when writing the report failed.
const exitWriteFailed = 2

// buildReport writes the report into b and returns the exit code.
func buildReport(b *strings.Builder, rep Report) int {
	if rep.BuildFailed {
		fmt.Fprintln(b, "BUILD FAILED — the suite did not compile, no test ran:")
		for _, l := range rep.BuildOutput {
			fmt.Fprintln(b, "  "+l)
		}
		return 1
	}
	if len(rep.Tests) == 0 && len(rep.PackageFailed) == 0 {
		fmt.Fprintln(b, "NO TESTS RAN — check the -run pattern and the build tags")
		return 1
	}
	printMatrix(b, rep.Tests)
	if len(rep.PackageFailed) > 0 {
		fmt.Fprintf(b, "\nPACKAGE FAILED outside any test: %s\n", strings.Join(rep.PackageFailed, ", "))
		for _, l := range rep.PackageOutput {
			fmt.Fprintln(b, "  "+l)
		}
	}
	if rep.Failed() {
		return 1
	}
	return 0
}

func main() {
	rep, err := Collect(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(Print(os.Stdout, rep))
}

// printMatrix prints one line per top-level test, grouped by spec family,
// then the totals line.
func printMatrix(w *strings.Builder, res map[string]result) {
	names := make([]string, 0, len(res))
	for n := range res {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		gi, gj := group(names[i]), group(names[j])
		if gi != gj {
			return gi < gj
		}
		return names[i] < names[j]
	})
	counts := map[State]int{}
	fmt.Fprintf(w, "%-16s %-8s %s\n", "SPEC", "STATE", "TEST")
	for _, n := range names {
		r := res[n]
		counts[r.state]++
		line := fmt.Sprintf("%-16s %-8s %s", group(n), r.state, n)
		if r.state == Pending && r.pending != "" {
			line += "   (" + r.pending + ")"
		}
		fmt.Fprintln(w, line)
		if r.state == Fail && r.failure != "" {
			f := r.failure
			if len(f) > maxFailureLine {
				f = f[:maxFailureLine] + "…"
			}
			fmt.Fprintln(w, "                          "+f)
		}
	}
	fmt.Fprintf(w, "\n%d pass · %d FAIL · %d pending · %d total\n", counts[Pass], counts[Fail], counts[Pending], len(names))
}
