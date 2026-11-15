package wire

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/lorehome"
)

// TestStoreHookCarriesTheHome (devco D-423): a store clone's pre-commit
// block runs lore under the LORE_HOME the installing build ran with, so a
// hand `git commit` in <home>/stores/<org>/<store> is checked against that
// home and not ~/.lore; with the default home the block is unchanged.
func TestStoreHookCarriesTheHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "fakelore")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"home=$LORE_HOME args=$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.ToSlash(filepath.Join(dir, "home with space", ".lore-x"))
	run := func(lines []string) string {
		t.Helper()
		cmd := exec.Command("sh", "-c", strings.Join(lines, "\n"))
		cmd.Env = append(os.Environ(), lorehome.EnvName+"=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("block: %v\n%s", err, out)
		}
		return string(out)
	}
	bin := "'" + fake + "'"
	if got := run(storeHookLines(bin, home)); !strings.Contains(got, "home="+home+" args=sync hook pre-commit") {
		t.Fatalf("the block must run lore under the installer's home %s:\n%s", home, got)
	}
	plain := storeHookLines(bin, "")
	if strings.Contains(strings.Join(plain, "\n"), lorehome.EnvName) {
		t.Fatalf("with the default home the block must not name %s:\n%s", lorehome.EnvName, strings.Join(plain, "\n"))
	}
	t.Setenv(lorehome.EnvName, home)
	if got := hookHome(); got != home {
		t.Fatalf("hookHome under %s=%s: %q", lorehome.EnvName, home, got)
	}
	t.Setenv(lorehome.EnvName, filepath.Join(dir, "it's"))
	if got := hookHome(); got != "" {
		t.Fatalf("a home with a quote cannot go into the hook text: %q", got)
	}
}
