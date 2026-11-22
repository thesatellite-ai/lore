package recall

import (
	"testing"
	"time"
)

// TestCallCPUCountsTheReadServer holds devco D-899 (bug hunt H-159): a
// served call's CPU is the client's plus the read server's share, so a
// slowdown in the server shows in the CPU column and a load burst (wall
// far above CPU) can be told from it.
func TestCallCPUCountsTheReadServer(t *testing.T) {
	client := 20 * time.Millisecond
	for _, c := range []struct {
		name          string
		before, after ServerSample
		want          time.Duration
	}{
		{"served by a running server", ServerSample{PID: 7, CPU: time.Second}, ServerSample{PID: 7, CPU: time.Second + 120*time.Millisecond}, 140 * time.Millisecond},
		{"the call started the server", ServerSample{}, ServerSample{PID: 9, CPU: 300 * time.Millisecond}, 320 * time.Millisecond},
		{"the call restarted the server", ServerSample{PID: 7, CPU: time.Second}, ServerSample{PID: 8, CPU: 250 * time.Millisecond}, 270 * time.Millisecond},
		{"no server (one-shot, or no /proc)", ServerSample{}, ServerSample{}, client},
		{"the server exited during the call", ServerSample{PID: 7, CPU: time.Second}, ServerSample{}, client},
	} {
		if got := CallCPU(client, c.before, c.after); got != c.want {
			t.Errorf("%s: CallCPU = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestStatCPU reads utime + stime of /proc/<pid>/stat lines, a command
// name with spaces and parentheses included.
func TestStatCPU(t *testing.T) {
	line := "4242 (lore (x) y) S 1 4242 4242 0 -1 4194560 9000 0 0 0 37 5 0 0 20 0 12 0 123456 1000000 2000 18446744073709551615"
	if got, ok := StatCPU(line); !ok || got != 420*time.Millisecond {
		t.Fatalf("StatCPU = %v, %v, want 420ms, true", got, ok)
	}
	for _, bad := range []string{"", "4242 lore S 1", "4242 (lore) S 1 2 3", "4242 (lore) S 1 4242 4242 0 -1 4194560 9000 0 0 0 x 5"} {
		if _, ok := StatCPU(bad); ok {
			t.Errorf("StatCPU(%q) = ok, want false", bad)
		}
	}
}
