// Package isolate runs one test (or subtest) of the current test binary in
// a child process of that same binary, so a test that must change
// process-wide state (environment variables through t.Setenv, time.Local, a
// package switch) can still run beside every other test instead of holding
// the whole package's serial phase.
//
// Why a child process and not a seam: wire's tests set HOME,
// GIT_CONFIG_GLOBAL, PATH, LORE_* and similar variables that are read by
// git subprocesses, ssh probes and identity lookups across many packages.
// t.Setenv refuses t.Parallel because the environment is one per process;
// a child process has its own, so the test body runs unchanged (same
// t.Setenv calls, same assertions) while the parent marks itself parallel.
//
// Second use, heavy SQLite tests: modernc.org/libc serialises every SQLite
// allocation of a process on one mutex (libc Xmalloc's allocMu), so in-process
// parallel tests that do a lot of SQLite work queue on that lock while the
// machine's cores sit idle. A test in its own process has its own lock. Use
// it only for the few heaviest tests; a child costs about 10 ms to start.
//
// Invariants:
//   - The child runs exactly the one test named by the parent (an anchored
//     -test.run pattern per name element) and the parent fails unless the
//     child reports that test as passed or skipped, so a pattern that
//     matches nothing can never pass silently.
//   - Only -test.run, -test.v, -test.short and -test.timeout reach the
//     child: profiles (-cpuprofile, -mutexprofile, …) stay with the parent,
//     so the child never overwrites a file the parent writes.
//   - The same binary is reused, so a -race parent runs a -race child and a
//     race report fails the child and through it the parent. The child gets
//     the parent's remaining -timeout (less a small slack) and -short; a
//     failure, panic, race report or timeout in the child fails the parent
//     test with the child's whole output, and under -v the parent logs the
//     child's output even when it passes.
//   - Under -cover (testing.CoverMode() != "") Run does not start a child: it
//     returns false, so the test runs in-process and serially, as it did
//     before this package existed, and its coverage is counted.
package isolate

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// childEnv names the environment variable that carries, into the child,
// the full name of the test the child was started for. Its presence is how
// a test knows it is the child and must run its body.
const childEnv = "LORE_ISOLATE_TEST"

// childTimeoutSlack is kept back from the parent's deadline so the parent
// can still report the child's output when the child runs out of time.
const childTimeoutSlack = 5 * time.Second

// Parent marks t parallel when its subtests will run in child processes
// through Run, and leaves it serial otherwise: inside a child, or under
// -cover where Run keeps subtests in-process, a subtest calls t.Setenv,
// which refuses a parallel ancestor.
func Parent(t *testing.T) {
	t.Helper()
	if _, inChild := os.LookupEnv(childEnv); inChild || testing.CoverMode() != "" {
		return
	}
	t.Parallel()
}

// Run runs t's test in a child process and reports whether it did. Use it
// as the first statement of the test or subtest:
//
//	if isolate.Run(t) {
//		return
//	}
//
// In the parent it marks t parallel, runs the child, copies its verdict
// (fail with the child's output, or skip) and returns true. In the child
// it returns false, so the body runs there serially, as before.
func Run(t *testing.T) bool {
	t.Helper()
	if os.Getenv(childEnv) == t.Name() || testing.CoverMode() != "" {
		return false
	}
	t.Parallel()
	args := []string{"-test.run=" + pattern(t.Name()), "-test.v=true"}
	if testing.Short() {
		args = append(args, "-test.short=true")
	}
	ctx := context.Background()
	if dl, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, dl)
		defer cancel()
		if left := time.Until(dl) - childTimeoutSlack; left > 0 {
			args = append(args, "-test.timeout="+left.String())
		}
	}
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"="+t.Name())
	out, err := cmd.CombinedOutput()
	switch {
	case strings.Contains(string(out), "--- SKIP: "+t.Name()+" "):
		t.Skipf("skipped in the child process:\n%s", out)
	case err != nil:
		t.Fatalf("child process for %s failed: %v\n%s", t.Name(), err, out)
	case !strings.Contains(string(out), "--- PASS: "+t.Name()+" "):
		t.Fatalf("child process did not run %s (pattern %s):\n%s", t.Name(), pattern(t.Name()), out)
	case testing.Verbose():
		t.Logf("child process output:\n%s", out)
	}
	return true
}

// pattern is the -test.run expression that matches name and nothing
// else: one anchored, quoted expression per slash-separated element.
func pattern(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	return strings.Join(parts, "/")
}
