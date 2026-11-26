//go:build bench && linux

package storegrowth

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ownRSSPoll is how often timedOwnRSS reads the process's peak.
const ownRSSPoll = 50 * time.Millisecond

// timedOwnRSS runs name in dir like timed and returns the peak RSS of
// that process alone in bytes (its VmHWM, read while it runs), never of
// the git processes it starts. Why (H-279): the rusage of a waited
// process counts its waited children, and a first sync's clone runs git's
// pack work (with a file:// remote, the server's too) under lore.
func (w *world) timedOwnRSS(dir, name string, args ...string) (result, int64) {
	ctx, cancel := context.WithTimeout(context.Background(), Limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, w.env
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	t0 := time.Now()
	if err := cmd.Start(); err != nil {
		return result{err: err}, 0
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	status := "/proc/" + strconv.Itoa(cmd.Process.Pid) + "/status"
	var peak int64
	tick := time.NewTicker(ownRSSPoll)
	defer tick.Stop()
	for {
		peak = max(peak, vmHWM(status))
		select {
		case err := <-done:
			r := result{wall: time.Since(t0), err: err, out: out.String()}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				r.over = true
			}
			return r, peak
		case <-tick.C:
		}
	}
}

// vmHWM is the VmHWM line of a /proc status file in bytes (0 when it
// cannot be read: the process has ended).
func vmHWM(status string) int64 {
	f, err := os.Open(status)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "VmHWM:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB")), 10, 64)
			if err != nil {
				return 0
			}
			return kb << 10
		}
	}
	return 0
}
