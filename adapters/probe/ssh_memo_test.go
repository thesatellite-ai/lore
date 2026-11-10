package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSSHAuthSharedInRun: in a run that shares remote answers (a doctor
// run's batch, WithRemoteMemo), github.ssh_auth and github.ssh_user of a
// setup, and every setup on the same route, cost one ssh -T; another route
// and a run without the memo each log in again.
func TestSSHAuthSharedInRun(t *testing.T) {
	s := newSandbox(t)
	log := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_LOG", log)
	fakeBin(t, s.bin, "ssh", `echo "$@" >> "$FAKE_LOG"
echo "Hi mira! You've successfully authenticated, but GitHub does not provide shell access." >&2; exit 1`)
	p := s.prober(Options{SSHTimeout: 30 * time.Second})
	calls := func() int {
		b, _ := os.ReadFile(log)
		return strings.Count(string(b), "\n")
	}

	run := WithRemoteMemo(context.Background())
	var wg sync.WaitGroup
	for range 4 { // the checks of one batch run at the same time
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := p.SSHAuth(run, Route{}); r.Class != ClassOK || r.User != "mira" {
				t.Errorf("shared ssh -T = %+v", r)
			}
		}()
	}
	wg.Wait()
	if n := calls(); n != 1 {
		t.Fatalf("one route in one run: %d ssh -T, want 1", n)
	}
	if r := p.SSHAuth(run, Route{HostAlias: "github-acme"}); r.Class != ClassOK {
		t.Fatalf("another route = %+v", r)
	}
	if n := calls(); n != 2 {
		t.Fatalf("a second route: %d ssh -T in all, want 2", n)
	}
	p.SSHAuth(context.Background(), Route{})
	if n := calls(); n != 3 {
		t.Fatalf("no memo: %d ssh -T in all, want 3", n)
	}
}
