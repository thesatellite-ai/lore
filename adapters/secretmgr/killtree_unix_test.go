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

	"github.com/thesatellite-ai/lore/internal/testtime"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestTimeoutKillsProcessTree (OV13b, SECRETS 6): a manager CLI that
// starts a helper of its own (a gpg-agent prompt, a vault login child)
// and then hangs loses the helper too when its time is up, not only the
// direct child; the call returns E_SECRET_UNAVAILABLE "timed out" well
// before the helper would have ended.
//
// devco D-415: the test armed a 300 ms timeout and hoped the helper had
// started by then; under load it had not ("the helper never started").
// The kill is the behaviour, so the test waits for the event instead: the
// helper writes its pid, then the call's context ends, which runs the same
// kill branch as the manager timeout (that the timeout itself fires is
// TestRevealUnavailable's hanging-CLI case). Only the "how long may
// that take" limits are run time, so only they scale.
func TestTimeoutKillsProcessTree(t *testing.T) {
	// the helper writes its pid, then sleeps; the CLI itself sleeps too
	scripts := map[string]string{}
	scripts[Gopass] = `d=$(dirname "$0"); sh -c "echo \$\$ > '$d/helper.pid.tmp' && mv '$d/helper.pid.tmp' '$d/helper.pid'; exec sleep 30" & sleep 30`
	look, dir := fakeCLIs(t, scripts)
	// the manager timeout is only a hang guard here: the context ends first
	set := Builtin(Options{LookPath: look, Timeout: testtime.Scale(20 * time.Second)})
	gp, _ := set.Get(Gopass)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		_, err := gp.Reveal(ctx, Locator{Path: "a/b"})
		done <- result{err}
	}()
	pidFile := filepath.Clean(filepath.Join(dir, "helper.pid"))
	var b []byte
	for wait := time.Now().Add(testtime.Scale(10 * time.Second)); ; time.Sleep(10 * time.Millisecond) {
		var err error
		if b, err = os.ReadFile(pidFile); err == nil {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("the call ended before the helper started: %v", r.err)
		default:
		}
		if time.Now().After(wait) {
			t.Fatal("the helper never started")
		}
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		t.Fatalf("bad helper pid %q", b)
	}
	cancel()
	select {
	case r := <-done:
		if !errs.Has(r.err, errs.VaultEntryUnavailable) || !strings.Contains(r.err.Error(), "timed out") {
			t.Fatalf("err = %v, want a timeout", r.err)
		}
	case <-time.After(testtime.Scale(5 * time.Second)):
		t.Fatal("the call did not return after its time was up")
	}
	deadline := time.Now().Add(testtime.Scale(3 * time.Second))
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
		time.Sleep(20 * time.Millisecond)
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
