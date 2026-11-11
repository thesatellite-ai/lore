package wire

// No ssh -T outlives the App: github.key_matches answers before its slow
// aliases do and leaves their probes running (so a re-run reads them from
// the cache), and App.Close ends every such probe with its process group
// before it returns. Before this, a `lore doctor` that exited left orphan
// ssh processes behind (each in its own process group, so not even ^C
// reached them), and the wire tests' TempDir clean-up raced them.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// leakSSHPrefix makes the fake ssh record its pid (one file per run) before
// it does anything else. On Windows that is the fakeexec shim's own pid,
// the process lore started: $$ there is an MSYS pid, not a Windows one.
const leakSSHPrefix = `mkdir -p "$HOME/pids"; : > "$HOME/pids/${FAKEEXEC_SHIM_PID:-$$}"
`

func TestKeyMatchesBackgroundProbesEndWithApp(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a fake ssh")
	}
	sp, home := aliasWorld(t)
	fakeexec.Write(t, filepath.Join(home, "bin", "ssh"), "#!/bin/sh\n"+leakSSHPrefix+fakeSSH+"\n", 0o755)
	cfg := strings.Replace(fakeSSHConfig, "Host octo-p\n  HostName github.com\n  IdentityFile ~/.ssh/octo-p\n", "", 1)
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r := sp.keyMatches(context.Background(), keyReq("github.com", "~/.ssh/work.pub"))
	if !strings.Contains(r.Detail, "github-slow") {
		t.Fatalf("the check did not leave github-slow probing: %s", r.Detail)
	}
	// github-slow (sleeps 10 s) is still probing in the background: wait
	// until its ssh has started, so Close has something to end.
	deadline := time.Now().Add(5 * time.Second)
	for sshCalls(t, home, "github-slow") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("github-slow was never probed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	if err := sp.a.Close(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Close took %v: it waited the slow alias out instead of ending it", took)
	}
	ents, err := os.ReadDir(filepath.Join(home, "pids"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		// processAlive (migrate_journal.go) is the per-OS liveness probe.
		if processAlive(pid) {
			t.Errorf("ssh pid %d is still running after App.Close", pid)
		}
	}
}
