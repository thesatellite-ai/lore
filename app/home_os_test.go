package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHomeVariableIsTheOneUserHomeDirReads: the home lore names in its
// refusals and hints is the variable os.UserHomeDir reads on this system
// (USERPROFILE on Windows, HOME elsewhere). Runs on every OS, so the
// Windows runner guards the USERPROFILE choice.
func TestHomeVariableIsTheOneUserHomeDirReads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(homeEnvName(), dir)
	got, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("set %s=%s but os.UserHomeDir is %q: lore names the wrong home variable", homeEnvName(), dir, got)
	}
}

// TestLoreHomeFileIsABrokenHome: a file where ~/.lore should be is a
// broken home on every system (Windows included, where the home is
// USERPROFILE): a write refuses with an E_ code naming .lore, no panic.
func TestLoreHomeFileIsABrokenHome(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t)
	repo := w.initRepo(t)
	home := w.env["HOME"]
	if err := os.RemoveAll(filepath.Join(home, ".lore")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".lore"), []byte("not a folder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := w.loreOut(t, repo, "memory", "add", "--body", "a note")
	if code == 0 {
		t.Fatalf("memory add with a file at ~/.lore succeeded:\n%s", out)
	}
	if strings.Contains(out, "panic") || !strings.Contains(out, "E_") || !strings.Contains(out, ".lore") {
		t.Fatalf("memory add with a file at ~/.lore: exit %d, want a clear E_ refusal naming .lore:\n%s", code, out)
	}
}
