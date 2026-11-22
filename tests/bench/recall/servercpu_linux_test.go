//go:build linux

package recall

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestServerCPUFindsTheReadServer starts a stand-in read server (a shell
// running a script named serve-reads: argv is `<sh> serve-reads`, as the
// real one's is `<lore> serve-reads`) for one home and checks ServerCPU
// finds it by its binary and HOME, sees the CPU it burns, and ignores it
// for another home (devco D-899).
func TestServerCPUFindsTheReadServer(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "i=0; while [ $i -lt 3000000 ]; do i=$((i+1)); done; sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "serve-reads"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sh, "serve-reads")
	cmd.Dir, cmd.Env = dir, []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	var s ServerSample
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if s = ServerCPU(sh, home); s.CPU >= 50*time.Millisecond {
			break
		}
	}
	if s.PID != cmd.Process.Pid || s.CPU < 50*time.Millisecond {
		t.Fatalf("ServerCPU = %+v, want pid %d with at least 50ms of CPU", s, cmd.Process.Pid)
	}
	if other := ServerCPU(sh, filepath.Join(dir, "other")); other != (ServerSample{}) {
		t.Fatalf("ServerCPU for another home = %+v, want none", other)
	}
}
