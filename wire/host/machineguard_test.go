package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/machineguard"
	opsorg "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// TestClientGuardUnderMachineGuard (devco H-119): with the machine guard
// on (the global core.hooksPath is lore's dispatcher folder), the link in
// a client repo writes lore's blocks where the dispatchers chain (the
// clone's .git/hooks), never into the dispatchers and never as the
// "shared hooks folder, add these lines by hand" refusal; doctor says the
// machine guard covers the clone.
func TestClientGuardUnderMachineGuard(t *testing.T) {
	h, errw, dir := clientGuardRepo(t)
	home := filepath.Join(os.Getenv("HOME"), ".lore")
	h.cfg.Home = home
	if _, err := machineguard.Install(context.Background(), gitref.Exec{}, home, false); err != nil {
		t.Fatal(err)
	}
	gd := machineguard.Dir(home)
	before := map[string]string{}
	for _, n := range clientGuardHookNames {
		b, _ := os.ReadFile(filepath.Join(gd, n))
		before[n] = string(b)
	}
	var notes []string
	h.doctorClientGuard(context.Background(), false, func(s string) { t.Fatalf("doctor warned under the machine guard: %s", s) }, func(s string) { notes = append(notes, s) })
	if got := strings.Join(notes, "\n"); !strings.Contains(got, "machine guard") {
		t.Fatalf("doctor does not say the machine guard covers the clone: %q", got)
	}
	h.installClientGuard(context.Background(), opsorg.OpLink)
	if strings.Contains(errw.String(), "NOT checked") {
		t.Fatalf("the link treated the machine guard's folder as a shared hooks folder: %s", errw)
	}
	for _, n := range clientGuardHookNames {
		if b, _ := os.ReadFile(filepath.Join(gd, n)); string(b) != before[n] {
			t.Fatalf("the link wrote into the machine guard's dispatcher %s:\n%s", n, b)
		}
		marker, _ := clientGuardBlock(n)
		if b, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", n)); err != nil || !strings.Contains(string(b), marker) {
			t.Fatalf("no lore block in .git/hooks/%s under the machine guard (err %v): %s", n, err, errw)
		}
	}
}

// TestMachineGuardChainedMarker: the dispatchers skip their own check
// when the chained hook already runs lore's block, found by the marker
// every lore block carries.
func TestMachineGuardChainedMarker(t *testing.T) {
	if !strings.HasPrefix(gitBlockMarker, machineguard.ChainedMarker) || !strings.HasPrefix(rideHookMarker, machineguard.ChainedMarker) {
		t.Fatalf("machineguard.ChainedMarker %q is not the start of lore's block markers %q, %q", machineguard.ChainedMarker, gitBlockMarker, rideHookMarker)
	}
}

// TestDoctorMachineGuard (devco H-119): doctor warns when the machine
// guard's hooks are missing (--fix rewrites them) and when the global
// core.hooksPath was changed away after the install; a machine that never
// opted in gets no line at all.
func TestDoctorMachineGuard(t *testing.T) {
	h, _, dir := clientGuardRepo(t)
	h.cfg.Home = filepath.Join(os.Getenv("HOME"), ".lore")
	ctx := context.Background()
	run := func(fix bool) (warns, notes []string) {
		h.doctorMachineGuard(ctx, fix, func(s string) { warns = append(warns, s) }, func(s string) { notes = append(notes, s) })
		return warns, notes
	}
	if w, n := run(false); len(w)+len(n) != 0 {
		t.Fatalf("doctor spoke of a machine guard never installed: %q %q", w, n)
	}
	if _, err := machineguard.Install(ctx, gitref.Exec{}, h.cfg.Home, false); err != nil {
		t.Fatal(err)
	}
	if w, n := run(false); len(w) != 0 || len(n) != 1 || !strings.Contains(n[0], "machine guard on") {
		t.Fatalf("doctor on an installed guard: warns %q notes %q", w, n)
	}
	gone := filepath.Join(machineguard.Dir(h.cfg.Home), "pre-commit")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if w, _ := run(false); len(w) != 1 || !strings.Contains(w[0], "pre-commit") || !strings.Contains(w[0], "lore setup guard install") {
		t.Fatalf("doctor did not warn about the missing pre-commit dispatcher: %q", w)
	}
	run(true)
	if _, err := os.Stat(gone); err != nil {
		t.Fatalf("doctor --fix did not rewrite the missing dispatcher: %v", err)
	}
	gitT(t, dir, "config", "--global", "core.hooksPath", "/opt/team-hooks")
	if w, _ := run(true); len(w) != 1 || !strings.Contains(w[0], "machine guard off") || !strings.Contains(w[0], `"/opt/team-hooks"`) {
		t.Fatalf("doctor did not warn that the global core.hooksPath changed away: %q", w)
	}
	if got, _ := exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output(); strings.TrimSpace(string(got)) != "/opt/team-hooks" {
		t.Fatalf("doctor --fix overwrote a changed global core.hooksPath: %q", got)
	}
}
