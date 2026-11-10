package teamharness

// deadline.go — a lore run that hangs names itself. Every run of the lore
// binary under test gets a deadline (CmdTimeout, TEAMHARNESS_CMD_TIMEOUT
// overrides it); past it the process gets SIGQUIT, so the Go runtime
// prints every goroutine of that lore to its stderr and exits, and the
// test fails naming the command, its folder and that dump. Before, a hang
// at high load showed only the test's own goroutines when go test's
// -timeout fired, never which lore command was stuck (journeys NEXT 3,
// teamlead run4 2026-11-09).

import (
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

// CmdTimeout is how long one lore run may take before it is stopped with
// a goroutine dump. Generous: a loaded gate machine makes a normal run
// several times slower; only a hang should reach it.
const CmdTimeout = 5 * time.Minute

// cmdKillGrace is how long a process that got SIGQUIT has to print its
// goroutines and exit before it is killed.
const cmdKillGrace = 10 * time.Second

// cmdTimeout is CmdTimeout, or TEAMHARNESS_CMD_TIMEOUT (a Go duration)
// when set and valid.
func cmdTimeout() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("TEAMHARNESS_CMD_TIMEOUT")); err == nil && d > 0 {
		return d
	}
	return CmdTimeout
}

// runWithDeadline runs cmd; past d it sends the dump signal (quitProcess)
// and, after cmdKillGrace, kills it. timedOut reports whether the deadline
// fired. Output pipes held open by a child of cmd (a git it started) are
// given up cmdKillGrace after cmd itself exits.
func runWithDeadline(cmd *exec.Cmd, d time.Duration) (timedOut bool, err error) {
	cmd.WaitDelay = cmdKillGrace
	if err = cmd.Start(); err != nil {
		return false, err
	}
	var fired atomic.Bool
	stop := time.AfterFunc(d, func() {
		fired.Store(true)
		quitProcess(cmd.Process)
		time.AfterFunc(cmdKillGrace, func() { _ = cmd.Process.Kill() })
	})
	err = cmd.Wait()
	stop.Stop()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // cmd exited; only a child of it still held the output open
	}
	return fired.Load(), err
}
