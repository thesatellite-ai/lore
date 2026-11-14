package host

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeLore writes an executable script at dir/name that prints tag and the
// LORE_HOME it sees.
func fakeLore(t *testing.T, dir, name, tag string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho \""+tag+" home=$LORE_HOME args=$*\"\n"), 0o755); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
	return p
}

// runPinLines runs lines as a hook would (sh, PATH=pathDir only, no
// LORE_HOME) and returns stdout.
func runPinLines(t *testing.T, pathDir string, lines []string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	cmd := exec.Command(sh, "-c", strings.Join(lines, "\n"))
	cmd.Env = []string{"PATH=" + pathDir + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook lines: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestHookPinRunsInstallingBuild (devco P-2): with an older `lore` first
// on PATH and a newer build run through a wrapper home (LORE_HOME), the
// hook blocks the newer build writes run THAT build with its LORE_HOME;
// once the build is gone they fall back to the `lore` on PATH. Without
// the pin (before the fix) the block ran the `lore` on PATH, which refused
// every commit of the newer build's rows.
func TestHookPinRunsInstallingBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	t.Parallel()
	pathDir, devDir := t.TempDir(), t.TempDir()
	fakeLore(t, pathDir, "lore", "PATH-LORE")
	dev := fakeLore(t, devDir, "lore", "DEV-BUILD")
	noPath := func(string) (string, error) { return filepath.Join(pathDir, "lore"), nil }
	tgt, ok := hookPinTarget(dev, "", "/home/someone/.lore-dev", noPath)
	if !ok {
		t.Fatal("a build that is not the lore on PATH must pin itself")
	}
	pin := tgt.line()
	lines := hookBlocksFor("", false, pin)[hookPostCommit].lines
	if lines[0] != pin {
		t.Fatalf("the pin must lead every block: %q", lines)
	}
	probe := []string{pin, "command -v lore >/dev/null 2>&1 && lore sync hook post-commit"}
	if got := runPinLines(t, pathDir, probe); !strings.HasPrefix(got, "DEV-BUILD home=/home/someone/.lore-dev") {
		t.Fatalf("the pinned block ran %q, want the dev build with its LORE_HOME", got)
	}
	if got := runPinLines(t, pathDir, probe[1:]); !strings.HasPrefix(got, "PATH-LORE") {
		t.Fatalf("an unpinned block must run the lore on PATH: %q", got)
	}
	back, ok := pinnedHookProgram("#!/bin/sh\n" + strings.Join(lines, "\n"))
	if !ok || back.prog != filepath.ToSlash(dev) || back.home != "/home/someone/.lore-dev" || back.line() != pin {
		t.Fatalf("pinnedHookProgram: %+v %v, want %s", back, ok, dev)
	}
	if err := os.Remove(dev); err != nil {
		t.Fatal(err)
	}
	if got := runPinLines(t, pathDir, probe); !strings.HasPrefix(got, "PATH-LORE home= ") {
		t.Fatalf("with the pinned build gone the block must fall back to the lore on PATH: %q", got)
	}
}

// TestHookPinTarget (devco P-2): which program the blocks name.
func TestHookPinTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("execute bits")
	}
	t.Parallel()
	dir := t.TempDir()
	self := fakeLore(t, dir, "lore", "SELF")
	wrapper := fakeLore(t, dir, "lore-dev", "WRAPPER")
	other := fakeLore(t, t.TempDir(), "lore", "OTHER")
	onPath := func(p string) func(string) (string, error) {
		return func(string) (string, error) { return p, nil }
	}
	none := func(string) (string, error) { return "", errors.New("not found") }
	cases := []struct {
		name, exe, bin, home string
		look                 func(string) (string, error)
		want                 hookTarget
		pinned               bool
	}{
		{"same file on PATH, default home: bare lore", self, "", "", onPath(self), hookTarget{}, false},
		{"same file on PATH, LORE_HOME set: pinned with the home", self, "", "/h/.lore-dev", onPath(self), hookTarget{prog: filepath.ToSlash(self), home: "/h/.lore-dev"}, true},
		{"another lore on PATH: pinned", self, "", "", onPath(other), hookTarget{prog: filepath.ToSlash(self)}, true},
		{"no lore on PATH: pinned", self, "", "", none, hookTarget{prog: filepath.ToSlash(self)}, true},
		{"LORE_HOOK_BIN names the wrapper: the wrapper", self, wrapper, "/h/.lore-dev", onPath(other), hookTarget{prog: filepath.ToSlash(wrapper)}, true},
		{"LORE_HOOK_BIN not executable: ignored", self, filepath.Join(dir, "missing"), "", onPath(self), hookTarget{}, false},
		{"a quote in LORE_HOME: bare", self, "", "/h/it's", onPath(other), hookTarget{}, false},
	}
	for _, c := range cases {
		got, ok := hookPinTarget(c.exe, c.bin, c.home, c.look)
		if ok != c.pinned || got != c.want {
			t.Errorf("%s: got %+v %v, want %+v %v", c.name, got, ok, c.want, c.pinned)
		}
	}
	if hooksDirInWorkTree("/r/.git", "/r/.git/hooks") || hooksDirInWorkTree("/r", "/r/.git/hooks") || !hooksDirInWorkTree("/r", "/r/.husky") {
		t.Error("hooksDirInWorkTree: .git/hooks is git's own folder whatever the top, .husky is in the work tree")
	}
	if hookPinFor("/r", "/r/.githooks", false) != "" || hookPinFor("/r", "/r/.git/hooks", true) != "" {
		t.Error("a hooks folder in the work tree or shared by every repo must never get a pin")
	}
}

// TestKeptSelfPin (devco P-2): blocks already pinned to this very
// executable are kept when bare would do (lore put on PATH later), so a
// later command rewrites no hook file; a pin naming another build is not.
func TestKeptSelfPin(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	self := hookTarget{prog: filepath.ToSlash(exe)}.line()
	if err := os.WriteFile(filepath.Join(dir, hookPreCommit), []byte("#!/bin/sh\n"+self+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := keptSelfPin(dir); !ok || got != self {
		t.Fatalf("keptSelfPin: %q %v, want this executable's pin", got, ok)
	}
	other := hookTarget{prog: "/elsewhere/lore"}.line()
	if err := os.WriteFile(filepath.Join(dir, hookPreCommit), []byte("#!/bin/sh\n"+other+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := keptSelfPin(dir); ok {
		t.Fatalf("keptSelfPin kept another build's pin: %q", got)
	}
}
