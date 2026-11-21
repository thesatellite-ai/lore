package host

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/fspath"
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

// TestHookPinRunsInstallingBuild (devco P-2, D-795): with an older `lore`
// first on PATH and a newer build run through a wrapper home (LORE_HOME),
// the hook blocks the newer build writes with --pin-hooks run THAT build
// with its LORE_HOME;
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
	tgt, ok := hookPinTarget(hookPinRun{exe: dev, home: "/home/someone/.lore-dev", asked: true, lookPath: noPath, scratch: scratchBuildPath})
	if !ok {
		t.Fatal("--pin-hooks run by a build that is not the lore on PATH must pin it")
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
	if !ok || back.prog != filepath.ToSlash(fspath.Canonical(dev)) || back.home != "/home/someone/.lore-dev" || back.line() != pin {
		t.Fatalf("pinnedHookProgram: %+v %v, want %s", back, ok, dev)
	}
	if err := os.Remove(dev); err != nil {
		t.Fatal(err)
	}
	if got := runPinLines(t, pathDir, probe); !strings.HasPrefix(got, "PATH-LORE home= ") {
		t.Fatalf("with the pinned build gone the block must fall back to the lore on PATH: %q", got)
	}
}

// TestHookPinTarget (devco P-2, D-795): which program the blocks name. The
// owner rule: hooks in a repo call `lore` from PATH; a build is pinned only
// on an explicit ask (--pin-hooks, or LORE_HOOK_BIN outside a tmp folder or
// a source tree with the default home). Before D-795 every build that was
// not the lore on PATH, or ran with LORE_HOME, pinned itself.
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
	noScratch := func(string) bool { return false }
	allScratch := func(string) bool { return true }
	selfT := filepath.ToSlash(fspath.Canonical(self))
	cases := []struct {
		name string
		run  hookPinRun
		want hookTarget
		ok   bool
	}{
		// D-795: no ask, no pin, whatever build runs
		{"no ask, a build that is not the lore on PATH: bare", hookPinRun{exe: self, lookPath: onPath(other), scratch: noScratch}, hookTarget{}, false},
		{"no ask, no lore on PATH: bare", hookPinRun{exe: self, lookPath: none, scratch: noScratch}, hookTarget{}, false},
		{"no ask, a wrapper's LORE_HOME alone: bare", hookPinRun{exe: self, home: "/h/.lore-dev", lookPath: onPath(other), scratch: noScratch}, hookTarget{}, false},
		{"--pin-hooks, a build that is not the lore on PATH: pinned", hookPinRun{exe: self, asked: true, lookPath: onPath(other), scratch: noScratch}, hookTarget{prog: selfT, asked: true}, true},
		{"--pin-hooks names a tmp or source-tree build and a LORE_HOME: pinned with it", hookPinRun{exe: self, home: "/h/.lore-dev", asked: true, lookPath: onPath(other), scratch: allScratch}, hookTarget{prog: selfT, home: "/h/.lore-dev", asked: true}, true},
		{"--pin-hooks run by the lore on PATH, default home: bare", hookPinRun{exe: self, asked: true, lookPath: onPath(self), scratch: noScratch}, hookTarget{}, false},
		{"LORE_HOOK_BIN alone, default home: the wrapper", hookPinRun{exe: self, hookBin: wrapper, lookPath: onPath(other), scratch: noScratch}, hookTarget{prog: filepath.ToSlash(wrapper), asked: true}, true},
		{"LORE_HOOK_BIN alone, LORE_HOME not the default: bare", hookPinRun{exe: self, hookBin: wrapper, home: "/h/.lore-dev", lookPath: onPath(other), scratch: noScratch}, hookTarget{}, false},
		{"LORE_HOOK_BIN alone in a tmp folder or source tree: bare", hookPinRun{exe: self, hookBin: wrapper, lookPath: onPath(other), scratch: allScratch}, hookTarget{}, false},
		{"LORE_HOOK_BIN with --pin-hooks: the wrapper with the home", hookPinRun{exe: self, hookBin: wrapper, home: "/h/.lore-dev", asked: true, lookPath: onPath(other), scratch: allScratch}, hookTarget{prog: filepath.ToSlash(wrapper), home: "/h/.lore-dev", asked: true}, true},
		{"LORE_HOOK_BIN not executable: ignored", hookPinRun{exe: self, hookBin: filepath.Join(dir, "missing"), lookPath: onPath(self), scratch: noScratch}, hookTarget{}, false},
		{"a quote in LORE_HOME: bare", hookPinRun{exe: self, home: "/h/it's", asked: true, lookPath: onPath(other), scratch: noScratch}, hookTarget{}, false},
	}
	for _, c := range cases {
		got, ok := hookPinTarget(c.run)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
	if hooksDirInWorkTree("/r/.git", "/r/.git/hooks") || hooksDirInWorkTree("/r", "/r/.git/hooks") || !hooksDirInWorkTree("/r", "/r/.husky") {
		t.Error("hooksDirInWorkTree: .git/hooks is git's own folder whatever the top, .husky is in the work tree")
	}
	asked := hookPinRun{exe: self, asked: true, lookPath: onPath(other), scratch: noScratch}
	if hookPinForRun(asked, "/r", "/r/.githooks", false) != "" || hookPinForRun(asked, "/r", "/r/.git/hooks", true) != "" {
		t.Error("a hooks folder in the work tree or shared by every repo must never get a pin")
	}
}

// writeHookPin writes a pre-commit file in dir holding pin.
func writeHookPin(t *testing.T, dir, pin string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, hookPreCommit), []byte("#!/bin/sh\n"+pin+"\nlore check leaks --staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestHookPinForKeepsOnlyAskedPins (devco D-795): a run with no ask keeps a
// pin the person asked for (another build's, while it is there), drops a
// pin an older lore wrote by itself (the `# lore-bin:` tag: a scratch
// worktree build that every commit then ran), and the lore on PATH drops
// every pin. Before D-795 the hooks kept a pin to whatever non-PATH build
// last wrote them.
func TestHookPinForKeepsOnlyAskedPins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("execute bits")
	}
	t.Parallel()
	hooks := filepath.Join(t.TempDir(), ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o750); err != nil {
		t.Fatal(err)
	}
	top := filepath.Dir(filepath.Dir(hooks))
	pathLore := fakeLore(t, t.TempDir(), "lore", "PATH-LORE")
	dev := fakeLore(t, t.TempDir(), "lore", "DEV")
	pinned := fakeLore(t, t.TempDir(), "lore", "PINNED")
	look := func(string) (string, error) { return pathLore, nil }
	noScratch := func(string) bool { return false }
	devRun := hookPinRun{exe: dev, home: "/h/.lore-dev", lookPath: look, scratch: noScratch}
	pathRun := hookPinRun{exe: pathLore, lookPath: look, scratch: noScratch}

	legacy := hookTarget{prog: filepath.ToSlash(pinned), home: "/h/.lore-dev"}
	writeHookPin(t, hooks, legacy.line())
	if got, ok := dirHookPin(hooks); !ok || got.asked || got.prog != legacy.prog {
		t.Fatalf("dirHookPin read %+v %v, want the legacy pin, not asked", got, ok)
	}
	if got := hookPinForRun(devRun, top, hooks, false); got != "" {
		t.Errorf("a pin an older lore wrote by itself must be dropped: %q", got)
	}

	asked := hookTarget{prog: filepath.ToSlash(pinned), home: "/h/.lore-dev", asked: true}
	writeHookPin(t, hooks, asked.line())
	if got := hookPinForRun(devRun, top, hooks, false); got != asked.line() {
		t.Errorf("a pin the person asked for must stay when another build writes with no ask: %q", got)
	}
	if got := hookPinForRun(pathRun, top, hooks, false); got != "" {
		t.Errorf("the lore on PATH must remove every pin: %q", got)
	}
	gone := hookTarget{prog: filepath.ToSlash(filepath.Join(t.TempDir(), "lore")), asked: true}
	writeHookPin(t, hooks, gone.line())
	if got := hookPinForRun(devRun, top, hooks, false); got != "" {
		t.Errorf("a pin to a build that is gone must be dropped: %q", got)
	}
	if !strings.Contains(pinRemovedLine(asked), "removed the pin to "+asked.prog+" (LORE_HOME=/h/.lore-dev); they run lore from PATH") {
		t.Errorf("pinRemovedLine: %q", pinRemovedLine(asked))
	}
}

// TestScratchBuildPath (devco D-795): a build in a tmp folder or inside a
// lore source tree is never pinned without --pin-hooks.
func TestScratchBuildPath(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, goModFile), []byte(loreModulePrefix+"\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		filepath.Join(src, "tmp", "bin", "lore"):            true,
		filepath.Join(os.TempDir(), "x", "lore"):            true,
		filepath.FromSlash("/srv/worktree/tmp/bin/lore"):    true,
		filepath.FromSlash("/opt/lore-tools/bin/lore"):      false,
		filepath.FromSlash("/usr/local/lib/lore-next/lore"): false,
	}
	for p, want := range cases {
		if got := scratchBuildPath(p); got != want {
			t.Errorf("scratchBuildPath(%s) = %v, want %v", p, got, want)
		}
	}
}
