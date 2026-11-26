//go:build bench && unix

package storegrowth

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// timedRSS runs name in dir like timed and also returns the process's
// peak RSS in bytes (doctor's memory bound, H-274).
func (w *world) timedRSS(dir, name string, args ...string) (result, int64) {
	ctx, cancel := context.WithTimeout(context.Background(), Limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, w.env
	t0 := time.Now()
	out, err := cmd.CombinedOutput()
	r := result{wall: time.Since(t0), err: err, out: string(out)}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		r.over = true
	}
	var rss int64
	if cmd.ProcessState != nil {
		if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			rss = int64(ru.Maxrss)
			if runtime.GOOS == "linux" {
				rss *= 1024 // linux reports kilobytes, darwin bytes
			}
		}
	}
	return r, rss
}
