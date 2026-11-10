//go:build !windows

package secretmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestTimeoutKillsProcessTree (OV13b, SECRETS 6): a manager CLI that
// starts a helper of its own (a gpg-agent prompt, a vault login child)
// and then hangs loses the helper too when the timeout fires, not only
// the direct child; the call returns E_SECRET_UNAVAILABLE "timed out"
// well before the helper would have ended.
func TestTimeoutKillsProcessTree(t *testing.T) {
	// the helper writes its pid, then sleeps; the CLI itself sleeps too
	scripts := map[string]string{}
	scripts[Gopass] = `d=$(dirname "$0"); sh -c "echo \$\$ > '$d/helper.pid'; exec sleep 30" & sleep 30`
	look, dir := fakeCLIs(t, scripts)
	set := Builtin(Options{LookPath: look, Timeout: 300 * time.Millisecond})
	gp, _ := set.Get(Gopass)
	start := time.Now()
	_, err := gp.Reveal(context.Background(), Locator{Path: "a/b"})
	if !errs.Has(err, errs.VaultEntryUnavailable) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the call took %s", d)
	}
	b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "helper.pid")))
	if err != nil {
		t.Fatalf("the helper never started: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		t.Fatalf("bad helper pid %q", b)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return // gone (killed and reaped)
		}
		if gone, _ := zombie(pid); gone {
			return // killed, waiting for init to reap it
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL) // clean up the leaked helper
			t.Fatalf("the helper (pid %d) survived the timeout", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// zombie reports whether pid is a killed process not yet reaped (Linux
// /proc state Z); false where /proc does not exist.
func zombie(pid int) (bool, error) {
	b, err := os.ReadFile(filepath.Clean("/proc/" + strconv.Itoa(pid) + "/stat"))
	if err != nil {
		return false, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i >= 0 && i+2 < len(s) && s[i+2] == 'Z', nil
}
