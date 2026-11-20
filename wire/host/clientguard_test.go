package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	opsorg "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// clientGuardRepo is a git clone with no .lore/ that resolves as a client
// repo, and a host whose stderr the test reads.
func clientGuardRepo(t *testing.T) (*liveHost, *strings.Builder, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // no global core.hooksPath from the machine running the test
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	errw := &strings.Builder{}
	h := &liveHost{cfg: LiveConfig{WorkDir: dir, Stderr: errw, ClientRepo: func(context.Context) bool { return true }}}
	return h, errw, dir
}

// TestClientGuardInstallsPrePush (devco:D-730): the link installs lore's
// pre-push block beside the commit-msg block in .git/hooks, so a commit
// made with --no-verify is still checked before it leaves the clone.
func TestClientGuardInstallsPrePush(t *testing.T) {
	h, errw, dir := clientGuardRepo(t)
	h.installClientGuard(context.Background(), opsorg.OpLink)
	for _, n := range clientGuardHookNames {
		marker, _ := clientGuardBlock(n)
		b, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", n))
		if err != nil || !strings.Contains(string(b), marker) {
			t.Fatalf("the link left no lore block in .git/hooks/%s (err %v):\n%s\nstderr: %s", n, err, b, errw)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".git", "hooks", hookPrePush)); !strings.Contains(string(b), "sync hook pre-push") {
		t.Fatalf("the pre-push block does not run the push leak check:\n%s", b)
	}
	if !strings.Contains(errw.String(), filepath.Join(".git", "hooks", hookPrePush)) {
		t.Fatalf("the install line does not name the pre-push hook: %s", errw)
	}
}

// TestClientGuardNeverWritesTrackedHooks (devco:D-730): a hooks folder
// committed with the client repo (core.hooksPath=.githooks) is never
// written; the link line and doctor name the lines to add instead.
func TestClientGuardNeverWritesTrackedHooks(t *testing.T) {
	h, errw, dir := clientGuardRepo(t)
	own := []byte("#!/bin/sh\nexit 0\n")
	if err := os.MkdirAll(filepath.Join(dir, ".githooks"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".githooks", hookCommitMsg), own, 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "config", "--local", "core.hooksPath", ".githooks")
	h.installClientGuard(context.Background(), opsorg.OpLink)
	if b, _ := os.ReadFile(filepath.Join(dir, ".githooks", hookCommitMsg)); string(b) != string(own) {
		t.Fatalf("lore changed the client's tracked commit-msg hook:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, ".githooks", hookPrePush)); err == nil {
		t.Fatal("lore created a pre-push hook in the client's tracked hooks folder")
	}
	for _, line := range clientGuardHandLines {
		if !strings.Contains(errw.String(), line) {
			t.Fatalf("the link line does not name %q to add by hand: %s", line, errw)
		}
	}
	var warns []string
	h.doctorClientGuard(context.Background(), true, func(s string) { warns = append(warns, s) }, func(string) {})
	got := strings.Join(warns, "\n")
	if !strings.Contains(got, "NOT checked") || !strings.Contains(got, clientGuardHandLines[hookPrePush]) {
		t.Fatalf("doctor --fix does not warn with the pre-push line to add: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".githooks", hookPrePush)); err == nil {
		t.Fatal("doctor --fix created a pre-push hook in the client's tracked hooks folder")
	}
}

// TestDoctorClientGuardMissingPrePush (devco:D-730): a clone linked
// before the pre-push block existed is a doctor warning naming `lore
// doctor --fix`, and --fix installs the block.
func TestDoctorClientGuardMissingPrePush(t *testing.T) {
	h, _, dir := clientGuardRepo(t)
	h.installClientGuard(context.Background(), opsorg.OpLink)
	if err := os.Remove(filepath.Join(dir, ".git", "hooks", hookPrePush)); err != nil {
		t.Fatal(err)
	}
	var warns []string
	warn := func(s string) { warns = append(warns, s) }
	h.doctorClientGuard(context.Background(), false, warn, func(string) {})
	if len(warns) != 1 || !strings.Contains(warns[0], "missing in "+hookPrePush) || !strings.Contains(warns[0], "`lore doctor --fix`") {
		t.Fatalf("doctor does not warn about the missing pre-push block with its fix: %q", warns)
	}
	warns = nil
	h.doctorClientGuard(context.Background(), true, warn, func(string) {})
	if b, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", hookPrePush)); err != nil || !strings.Contains(string(b), rideHookMarker) {
		t.Fatalf("doctor --fix did not install the pre-push block (err %v, lines %q)", err, warns)
	}
}

// TestDoctorClientGuardNeverLinkedIsANote (devco:D-775): a teammate's
// clone of a client repo the store links by its remote, where the link
// never ran, holds no guard block and no lore.clientrepo mark; doctor
// says so in a note naming `lore doctor --fix`, never a warning (a member
// who only joined is not a degraded machine), and --fix installs it.
// Once the guard was on, a removed block is a warning again.
func TestDoctorClientGuardNeverLinkedIsANote(t *testing.T) {
	h, _, dir := clientGuardRepo(t)
	var warns, notes []string
	warn := func(s string) { warns = append(warns, s) }
	note := func(s string) { notes = append(notes, s) }
	h.doctorClientGuard(context.Background(), false, warn, note)
	if len(warns) != 0 {
		t.Fatalf("doctor warns in a clone the link never ran in: %q", warns)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "leak guard off in this clone") || !strings.Contains(notes[0], "`lore doctor --fix`") {
		t.Fatalf("doctor notes %q, want one saying the guard is off and naming `lore doctor --fix`", notes)
	}
	warns, notes = nil, nil
	h.doctorClientGuard(context.Background(), true, warn, note)
	for _, n := range clientGuardHookNames {
		marker, _ := clientGuardBlock(n)
		if b, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", n)); err != nil || !strings.Contains(string(b), marker) {
			t.Fatalf("doctor --fix did not install the %s block (err %v, lines %q)", n, err, warns)
		}
	}
	for _, n := range clientGuardHookNames {
		if err := os.Remove(filepath.Join(dir, ".git", "hooks", n)); err != nil {
			t.Fatal(err)
		}
	}
	warns, notes = nil, nil
	h.doctorClientGuard(context.Background(), false, warn, note)
	if len(warns) != 1 || !strings.Contains(warns[0], "leak guard missing in "+hookCommitMsg+", "+hookPrePush) {
		t.Fatalf("doctor does not warn when both blocks left a clone the guard was on in: warnings %q, notes %q", warns, notes)
	}
}
