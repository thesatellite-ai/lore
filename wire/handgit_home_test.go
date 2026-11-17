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
	if got := run(storeHookLines(bin, storeHookHome{abs: home})); !strings.Contains(got, "home="+home+" args=sync hook pre-commit") {
		t.Fatalf("the block must run lore under the installer's home %s:\n%s", home, got)
	}
	plain := storeHookLines(bin, storeHookHome{})
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

// TestStoreHookFollowsItsHome (devco D-534, weak 10): a store clone's
// block finds its lore home from where the clone sits, so a home that was
// copied elsewhere checks against the copy; it runs the build that wrote
// it only while that file is there, then the first lore on PATH (before, a
// block naming a removed build refused every hand commit), and so does the
// merge driver.
func TestStoreHookFollowsItsHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh")
	}
	dir := t.TempDir()
	pathDir := filepath.Join(dir, "path")
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pathDir, "lore"), []byte("#!/bin/sh\necho \"onpath home=$LORE_HOME args=$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	build := filepath.Join(dir, "build", "lore-preview")
	if err := os.MkdirAll(filepath.Dir(build), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(build, []byte("#!/bin/sh\necho \"build home=$LORE_HOME args=$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "first-home")
	t.Setenv(lorehome.EnvName, home)
	clone := filepath.Join(home, "stores", "acme", "notes")
	spec := storeHookHomeFor(clone)
	if spec.up != 3 {
		t.Fatalf("a clone at stores/acme/notes is 3 folders under its home, got %+v", spec)
	}
	lines := storeHookLines(storeHookProg("'"+build+"'", ""), spec)
	if strings.Contains(strings.Join(lines, "\n"), home) {
		t.Fatalf("the block must not name the home it was written under:\n%s", strings.Join(lines, "\n"))
	}
	// the same clone in a copy of the home
	copied := filepath.Join(dir, "copied-home")
	at := filepath.Join(copied, "stores", "acme", "notes")
	if err := os.MkdirAll(at, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(script string) string {
		t.Helper()
		cmd := exec.Command("sh", "-c", script)
		cmd.Dir = at
		cmd.Env = append(os.Environ(), lorehome.EnvName+"=", "PATH="+pathDir+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("block: %v\n%s", err, out)
		}
		return string(out)
	}
	wantHome, err := filepath.EvalSymlinks(copied)
	if err != nil {
		t.Fatal(err)
	}
	if got := run(strings.Join(lines, "\n")); !strings.Contains(got, "build home="+wantHome+" args=sync hook pre-commit") {
		t.Fatalf("the block must run the build under the home the clone sits in (%s):\n%s", wantHome, got)
	}
	if err := os.Remove(build); err != nil {
		t.Fatal(err)
	}
	if got := run(strings.Join(lines, "\n")); !strings.Contains(got, "onpath home="+wantHome+" args=sync hook pre-commit") {
		t.Fatalf("with the build gone the block must run the lore on PATH:\n%s", got)
	}
	drv := strings.NewReplacer("%O", "o", "%A", "a", "%B", "b", "%P", "p").Replace(storeDriverLine(storeHookProg("'"+build+"'", "")))
	if got := run(drv); !strings.Contains(got, "onpath") || !strings.Contains(got, "args=merge-driver o a b p") {
		t.Fatalf("with the build gone the merge driver must run the lore on PATH:\n%s", got)
	}
}
