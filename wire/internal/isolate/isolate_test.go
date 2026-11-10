package isolate

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// selfTestEnv switches on the fixture tests below. They are run only by
// TestRunPassesTheChildVerdictUp, in a fresh copy of this test binary, so a
// plain `go test` of this package never sees them fail.
const selfTestEnv = "LORE_ISOLATE_SELFTEST"

// fixture skips unless this process was started by the verdict test.
func fixture(t *testing.T) string {
	t.Helper()
	mode := os.Getenv(selfTestEnv)
	if mode == "" {
		t.Skip("fixture: run by TestRunPassesTheChildVerdictUp")
	}
	return mode
}

func TestFixtureIsolated(t *testing.T) {
	mode := fixture(t)
	if Run(t) {
		return
	}
	// The body runs only in the child process.
	switch mode {
	case "fail":
		t.Fatal("fixture body failed")
	case "panic":
		panic("fixture body panicked")
	case "setenv":
		t.Setenv("LORE_ISOLATE_FIXTURE", "1") // refused next to t.Parallel; allowed in the child
	}
}

// TestRunPassesTheChildVerdictUp pins the contract: a body that fails or
// panics in the child fails the parent test with the child's output; a
// body that passes (here one that calls t.Setenv, which a parallel test may
// not) passes; the child runs the one test exactly once.
func TestRunPassesTheChildVerdictUp(t *testing.T) {
	if os.Getenv(selfTestEnv) != "" || os.Getenv(childEnv) != "" {
		t.Skip("inside a fixture run")
	}
	for _, c := range []struct {
		mode     string
		wantFail bool
		wantOut  string
	}{
		{"fail", true, "fixture body failed"},
		{"panic", true, "fixture body panicked"},
		{"setenv", false, "--- PASS: TestFixtureIsolated"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(os.Args[0], "-test.run=^TestFixtureIsolated$", "-test.v=true")
			cmd.Env = append(os.Environ(), selfTestEnv+"="+c.mode)
			out, err := cmd.CombinedOutput()
			if (err != nil) != c.wantFail {
				t.Fatalf("exit err = %v, want failure %v:\n%s", err, c.wantFail, out)
			}
			if !strings.Contains(string(out), c.wantOut) {
				t.Fatalf("output lacks %q:\n%s", c.wantOut, out)
			}
			if c.wantFail && !strings.Contains(string(out), "child process for TestFixtureIsolated failed") {
				t.Fatalf("the parent did not report the child's failure:\n%s", out)
			}
			// One parent run plus one child run of the body, never more.
			if n := strings.Count(string(out), "=== RUN   TestFixtureIsolated"); n != 2 {
				t.Fatalf("TestFixtureIsolated started %d times, want 2 (parent + child):\n%s", n, out)
			}
		})
	}
}

func TestPattern(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"TestA":            "^TestA$",
		"TestA/sub_name":   "^TestA$/^sub_name$",
		"TestA/a,_b.(c)+d": `^TestA$/^a,_b\.\(c\)\+d$`,
	} {
		if got := pattern(in); got != want {
			t.Errorf("pattern(%q) = %q, want %q", in, got, want)
		}
	}
}
