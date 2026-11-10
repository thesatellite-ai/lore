//go:build !windows

package teamharness

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestHelperHang is not a test: TestRunWithDeadlineDumps starts this test
// binary with TEAMHARNESS_HELPER_HANG=1 as a stand-in for a lore that hangs.
func TestHelperHang(t *testing.T) {
	if os.Getenv("TEAMHARNESS_HELPER_HANG") != "1" {
		t.Skip("helper process only")
	}
	helperHangsHere()
}

func helperHangsHere() { time.Sleep(time.Hour) }

// TestRunWithDeadlineDumps: a run past its deadline is stopped, and its
// stderr holds the goroutine dump naming where it was stuck.
func TestRunWithDeadlineDumps(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHang$")
	cmd.Env = append(os.Environ(), "TEAMHARNESS_HELPER_HANG=1")
	var errb bytes.Buffer
	cmd.Stderr = &errb
	start := time.Now()
	timedOut, err := runWithDeadline(cmd, 2*time.Second)
	if !timedOut || err == nil {
		t.Fatalf("timedOut=%v err=%v; want a stopped run", timedOut, err)
	}
	if time.Since(start) > 2*time.Second+cmdKillGrace+5*time.Second {
		t.Errorf("the hung run took %s to stop", time.Since(start))
	}
	if s := errb.String(); !strings.Contains(s, "goroutine ") || !strings.Contains(s, "helperHangsHere") {
		t.Errorf("no goroutine dump naming the stuck function in stderr:\n%s", s)
	}
}

// TestRunWithDeadlineQuick: a run inside its deadline is untouched.
func TestRunWithDeadlineQuick(t *testing.T) {
	t.Parallel()
	timedOut, err := runWithDeadline(exec.Command("true"), time.Minute)
	if timedOut || err != nil {
		t.Fatalf("timedOut=%v err=%v", timedOut, err)
	}
}
