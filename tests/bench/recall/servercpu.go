package recall

// servercpu.go — the CPU a lore call spent in its home's read server
// (devco D-899, bug hunt H-159).
//
// Why: a warm or cold `lore ask` is answered by the read server
// (`lore serve-reads`), a separate process; the client only forwards it.
// The bench's CPU column counted the client alone (~20 ms, the start of a
// binary), so a real slowdown in the server never showed there, and a gate
// that missed its bars on a loaded box (v0.1.33: load 86 on a 48-core box,
// 30 bars) could not be told from a code regression by its report: both
// read "CPU 22-37 ms". With the server's CPU added, a load burst reads as
// wall time far above CPU time and a slowdown as CPU time that grew.
//
//	before the call ─▶ ServerCPU(bin, home): the server's user+system time
//	after the call  ─▶ ServerCPU again
//	CallCPU(client, before, after) = client + the server's share of the call

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ServerSample is the read server of one lore home at one moment: its pid
// and the CPU time (user + system) it has used so far. The zero value is
// "no server running" (or a platform without /proc).
type ServerSample struct {
	PID int
	CPU time.Duration
}

// CallCPU is the CPU time of one lore call: the client process's own
// plus what the read server spent between before and after. When the
// server changed during the call (it was started, or restarted, by it) the
// new server's whole CPU counts: it lived only inside the call.
func CallCPU(client time.Duration, before, after ServerSample) time.Duration {
	switch {
	case after.PID == 0:
		return client
	case after.PID != before.PID:
		return client + after.CPU
	case after.CPU > before.CPU:
		return client + after.CPU - before.CPU
	}
	return client
}

// clockTick is the unit of utime and stime in /proc/<pid>/stat
// (sysconf(_SC_CLK_TCK), 100 on every Linux the bench runs on).
const clockTick = 10 * time.Millisecond

// ServerCPU samples the read server that bin runs for home: the process
// whose command line is `<bin> serve-reads` and whose HOME is home. It
// reads /proc, so off Linux it always returns the zero ServerSample (the
// CPU column is then the client's alone, as before).
func ServerCPU(bin, home string) ServerSample {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return ServerSample{}
	}
	exe := resolved(bin)
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join("/proc", e.Name())
		if !servesHome(dir, exe, home) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "stat")) // #nosec G304 -- a /proc path built from a pid
		if err != nil {
			continue
		}
		if cpu, ok := StatCPU(string(raw)); ok {
			return ServerSample{PID: pid, CPU: cpu}
		}
	}
	return ServerSample{}
}

// resolved is bin as /proc/<pid>/exe names it: absolute, links followed
// (the server starts itself from os.Executable, not from the path typed).
func resolved(bin string) string {
	if abs, err := filepath.Abs(bin); err == nil {
		bin = abs
	}
	if real, err := filepath.EvalSymlinks(bin); err == nil {
		bin = real
	}
	return bin
}

// servesHome reports whether the process in dir (/proc/<pid>) runs exe
// as `serve-reads` with HOME=home.
func servesHome(dir, exe, home string) bool {
	if got, err := os.Readlink(filepath.Join(dir, "exe")); err != nil || got != exe {
		return false
	}
	cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline")) // #nosec G304 -- a /proc path built from a pid
	if err != nil {
		return false
	}
	args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	if len(args) < 2 || args[1] != "serve-reads" {
		return false
	}
	env, err := os.ReadFile(filepath.Join(dir, "environ")) // #nosec G304 -- a /proc path built from a pid
	if err != nil {
		return false
	}
	for kv := range bytes.SplitSeq(env, []byte{0}) {
		if string(kv) == "HOME="+home {
			return true
		}
	}
	return false
}

// StatCPU is the user + system time of a /proc/<pid>/stat line (fields 14
// and 15, counted after the command name in parentheses, which may itself
// hold spaces); false when the line has another shape.
func StatCPU(stat string) (time.Duration, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(stat[i+1:])
	// f[0] is field 3 (state): utime is field 14 = f[11], stime f[12]
	if len(f) < 13 {
		return 0, false
	}
	u, err1 := strconv.ParseInt(f[11], 10, 64)
	s, err2 := strconv.ParseInt(f[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return time.Duration(u+s) * clockTick, true
}
