package host

// hookbin.go — which lore a clone's git hook blocks run (devco P-2).
//
// The blocks call `lore`. With two builds on one machine (a released
// `lore` on PATH and a newer build run as `lore-dev` through a wrapper that
// sets LORE_HOME), the first `lore` on PATH is the older one: it cannot read
// the newer build's rows and refused every commit of them with a step the
// person could not take (install the new build as `lore`). So a build that
// writes the blocks into the clone's own hooks folder records itself in
// them: one pin line per block defines a shell function `lore` that runs
// that executable (with the LORE_HOME it ran with), only while the file is
// there. Every other line of the block is unchanged, so with the build gone
// the block falls back to the first `lore` on PATH, as before.
//
//	running build ─▶ LORE_HOOK_BIN names an executable (a wrapper's own path)? ─▶ pin it
//	              ─▶ else `lore` on PATH is this very file and no LORE_HOME? ─▶ no pin (bare `lore`)
//	              ─▶ else pin this executable (+ LORE_HOME when it was set)
//
// DECISION (P-2): no pin in a hooks folder git shares across repos
// (core.hooksPath in the global git config) or in one inside the work tree
// (husky's .husky, a committed .githooks): those files are every repo's or
// every teammate's, and one person's binary path must not land in them.
// Hooks in .git/hooks are per clone and never committed, so a 0.1.17
// teammate's clone never sees the pin.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/reposync"
	"github.com/thesatellite-ai/lore/model/fspath"
)

// EnvHookBin names the program a wrapper wants lore's git hooks to run (the
// wrapper's own absolute path, e.g. a `lore-dev` script that sets LORE_HOME
// and execs a preview build): when it names an executable file, the hook
// pin runs it instead of the binary the wrapper started.
const EnvHookBin = "LORE_HOOK_BIN"

// hookPinTag ends every pin line, so a person reading the hook (and
// pinnedHookProgram) can tell lore's pin from other text.
const hookPinTag = "# lore-bin: the lore build that set up these hooks; the first lore on PATH when it is gone"

// shellQuoteChar is the quote hook pin values are wrapped in; a value
// holding it is never pinned (it could not be quoted safely).
const shellQuoteChar = "'"

// hookPinRe reads the program and the optional LORE_HOME back out of a pin
// line (doctor).
var hookPinRe = regexp.MustCompile(`if \[ -x '([^']*)' \]; then lore\(\) \{ (?:LORE_HOME='([^']*)' )?'[^']*' "\$@"; \}; fi( # lore-pin:)?`)

// hookPinAskedTag ends a pin the person asked for (devco D-795): written by
// a run with --pin-hooks or with LORE_HOOK_BIN set. A later run of another
// build keeps it; the lore on PATH removes it.
const hookPinAskedTag = "# lore-pin: pinned on request (--pin-hooks or LORE_HOOK_BIN); the first lore on PATH when it is gone"

// pinHooksFlag is the flag of lore fix, doctor --fix and sync that pins
// the clone's hook blocks to the build running it (devco D-795).
const pinHooksFlag = "pin-hooks"

// unpinHooksCommand removes a pin: lore fix run by the lore on PATH writes
// bare blocks.
const unpinHooksCommand = "lore fix"

// tmpPathElem is a folder name a build is never pinned from without
// --pin-hooks (a scratch build in a worktree's tmp/bin).
const tmpPathElem = "tmp"

// loreModulePrefix starts the go.mod module line of a lore source tree; a
// build inside one is never pinned without --pin-hooks.
const loreModulePrefix = "module github.com/thesatellite-ai/lore"

// goModFile is the file that marks a Go source tree.
const goModFile = "go.mod"

// hookTarget is the program a pin runs and the LORE_HOME it sets ("" for
// none); asked is true for a pin the person asked for (hookPinAskedTag).
type hookTarget struct {
	prog, home string
	asked      bool
}

// hookPinRun is what decides the pin of one run: the running executable, the
// value of LORE_HOOK_BIN, the LORE_HOME it runs with ("" for the default
// home) and whether the person passed --pin-hooks.
type hookPinRun struct {
	exe, hookBin, home string
	asked              bool
	lookPath           func(string) (string, error)
	// scratch reports a path in a tmp folder or a lore source tree
	// (scratchBuildPath; a test passes its own).
	scratch func(string) bool
}

// hookPinTarget is the pin a run writes (devco D-795, owner rule: hooks in
// a repo call `lore` from PATH; a build is pinned only when the person
// asks for it there). ok is false when the blocks call bare `lore`.
//
//	--pin-hooks          ─▶ pin LORE_HOOK_BIN (when it names an executable) or this build, with its LORE_HOME
//	LORE_HOOK_BIN alone  ─▶ pin it, unless it sits in a tmp folder or a lore source tree, or LORE_HOME is not the default
//	neither              ─▶ no pin (a wrapper that only sets LORE_HOME, a scratch build: bare `lore`)
//
// The lore on PATH with the default home is never pinned: bare runs it.
func hookPinTarget(r hookPinRun) (hookTarget, bool) {
	if strings.Contains(r.home, shellQuoteChar) {
		return hookTarget{}, false
	}
	viaEnv := r.hookBin != "" && filepath.IsAbs(r.hookBin) && executableHookFile(r.hookBin) && !strings.Contains(r.hookBin, shellQuoteChar)
	if !viaEnv && !r.asked {
		return hookTarget{}, false // no opt-in: bare `lore`, whatever build runs
	}
	t := hookTarget{home: r.home, asked: true}
	if viaEnv {
		t.prog = filepath.ToSlash(filepath.Clean(r.hookBin))
	} else {
		exe := r.exe
		if exe == "" || runsAsPathLore(exe, r.home, r.lookPath) {
			return hookTarget{}, false // `lore` on PATH is this file: bare survives an upgrade in place
		}
		if res, err := filepath.EvalSymlinks(exe); err == nil {
			exe = res
		}
		if strings.Contains(exe, shellQuoteChar) {
			return hookTarget{}, false
		}
		t.prog = filepath.ToSlash(exe)
	}
	if !r.asked && (t.home != "" || r.scratch(filepath.FromSlash(t.prog))) {
		return hookTarget{}, false // only --pin-hooks names these explicitly
	}
	return t, true
}

// runsAsPathLore reports whether exe with home ("" for the default) is the
// lore on PATH run as itself: the build that never pins and removes pins.
func runsAsPathLore(exe, home string, lookPath func(string) (string, error)) bool {
	if home != "" || exe == "" {
		return false
	}
	if res, err := filepath.EvalSymlinks(exe); err == nil {
		exe = res
	}
	return reposync.DriverBinary(exe, lookPath) == reposync.BinaryName
}

// scratchBuildPath reports whether p is in a tmp folder (a path element
// named tmp, or under the system temp folder) or inside a lore source tree
// (a folder above it holds lore's go.mod): a build there is replaced or
// removed at any time, so it is never pinned without --pin-hooks.
func scratchBuildPath(p string) bool {
	p = filepath.Clean(p)
	for _, el := range strings.Split(filepath.ToSlash(p), "/") {
		if el == tmpPathElem {
			return true
		}
	}
	if tmp := os.TempDir(); tmp != "" {
		if _, inside := fspath.Rel(filepath.Clean(tmp), p); inside {
			return true
		}
	}
	for dir := filepath.Dir(p); ; dir = filepath.Dir(dir) {
		if b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, goModFile))); err == nil && strings.HasPrefix(strings.TrimSpace(string(b)), loreModulePrefix) {
			return true
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// line is the pin line chained first into each block.
func (t hookTarget) line() string {
	env := ""
	if t.home != "" {
		env = "LORE_HOME=" + shellQuoteChar + t.home + shellQuoteChar + " "
	}
	q := shellQuoteChar + t.prog + shellQuoteChar
	tag := hookPinTag
	if t.asked {
		tag = hookPinAskedTag
	}
	return "if [ -x " + q + " ]; then " + binaryName + "() { " + env + q + ` "$@"; }; fi ` + tag
}

// describe is the pinned program for a doctor line, with its LORE_HOME.
func (t hookTarget) describe() string {
	if t.home == "" {
		return t.prog
	}
	return t.prog + " (LORE_HOME=" + t.home + ")"
}

// thisRun is the hookPinRun of this process.
func thisRun(ctx context.Context) hookPinRun {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	return hookPinRun{exe: exe, hookBin: os.Getenv(EnvHookBin), home: runningHome(), asked: pinHooksAsked(ctx), lookPath: exec.LookPath, scratch: scratchBuildPath}
}

// runningHome is this process's LORE_HOME, absolute and with slashes; ""
// when unset or when it names the default home (~/.lore).
func runningHome() string {
	home := os.Getenv(lorehome.EnvName)
	if home == "" {
		return ""
	}
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	if uh, err := os.UserHomeDir(); err == nil && uh != "" && filepath.Clean(home) == filepath.Join(uh, lorehome.DirName) {
		return ""
	}
	return filepath.ToSlash(home)
}

// hookPinFor is the pin line for the hooks folder dir of the clone at top
// ("" when the folder is shared by every repo or is in the work tree, see
// the DECISION above). With no opt-in in this run, a pin the person asked
// for earlier stays while its program is there, unless this run is the
// lore on PATH (devco D-795: it writes bare blocks, removing every pin,
// and says so).
func hookPinFor(ctx context.Context, top, dir string, shared bool) string {
	return hookPinForRun(thisRun(ctx), top, dir, shared)
}

// hookPinForRun is hookPinFor for the run r.
func hookPinForRun(r hookPinRun, top, dir string, shared bool) string {
	if shared || hooksDirInWorkTree(top, dir) {
		return ""
	}
	if t, ok := hookPinTarget(r); ok {
		return t.line()
	}
	if r.asked || runsAsPathLore(r.exe, r.home, r.lookPath) {
		return ""
	}
	if t, ok := dirHookPin(dir); ok && t.asked && executableHookFile(filepath.FromSlash(t.prog)) {
		return t.line()
	}
	return ""
}

// dirHookPin is the pin of lore's blocks in the hooks folder dir (the
// first hook that holds one).
func dirHookPin(dir string) (hookTarget, bool) {
	for _, n := range []string{hookPreCommit, hookCommitMsg, hookPostCommit, hookPrePush} {
		b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, n)))
		if err != nil {
			continue
		}
		if t, ok := pinnedHookProgram(string(b)); ok {
			return t, true
		}
	}
	return hookTarget{}, false
}

// pinRemovedLine is what a run that removed a pin says (devco D-795).
func pinRemovedLine(old hookTarget) string {
	return "hooks: removed the pin to " + old.describe() + "; they run lore from PATH"
}

// pinHooksKey marks a context whose run passed --pin-hooks.
type pinHooksKey struct{}

// withPinHooks marks ctx: this run (lore fix, doctor --fix or sync with
// --pin-hooks) pins the hook blocks to its build.
func withPinHooks(ctx context.Context) context.Context {
	return context.WithValue(ctx, pinHooksKey{}, true)
}

// pinHooksAsked reports whether ctx's run passed --pin-hooks.
func pinHooksAsked(ctx context.Context) bool {
	v, ok := ctx.Value(pinHooksKey{}).(bool)
	return ok && v
}

// hooksDirInWorkTree reports whether dir is inside the work tree at top and
// not in its .git folder (a committed or committable hooks folder).
func hooksDirInWorkTree(top, dir string) bool {
	// a folder inside a .git folder is git's own, never committed: a run
	// whose top is the git folder itself (a hook's or a store's session)
	// must not read .git/hooks as a work-tree folder and drop the pin
	for _, el := range strings.Split(filepath.ToSlash(filepath.Clean(dir)), "/") {
		if el == ".git" {
			return false
		}
	}
	rel, inside := fspath.Rel(top, dir)
	if !inside {
		return false
	}
	return strings.SplitN(filepath.ToSlash(rel), "/", 2)[0] != ".git"
}

// withHookPin is lines with pin first (lines unchanged when pin is "").
func withHookPin(pin string, lines []string) []string {
	if pin == "" {
		return lines
	}
	return append([]string{pin}, lines...)
}

// pinnedHookProgram reads the pin out of a hook file's content: ok is false
// when lore's blocks there call bare `lore`.
func pinnedHookProgram(content string) (hookTarget, bool) {
	m := hookPinRe.FindStringSubmatch(content)
	if m == nil {
		return hookTarget{}, false
	}
	return hookTarget{prog: m[1], home: m[2], asked: m[3] != ""}, true
}

// executableHookFile reports whether p is a regular file someone may run.
func executableHookFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&rideExecBits != 0
}
