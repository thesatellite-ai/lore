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
var hookPinRe = regexp.MustCompile(`if \[ -x '([^']*)' \]; then lore\(\) \{ (?:LORE_HOME='([^']*)' )?'[^']*' "\$@"; \}; fi`)

// hookTarget is the program a pin runs and the LORE_HOME it sets ("" for
// none).
type hookTarget struct {
	prog, home string
}

// hookPinTarget is the pin for a build: exe is the running executable,
// loreBin the value of LORE_HOOK_BIN, home the value of LORE_HOME ("" when
// unset). ok is false when the blocks should call bare `lore`.
func hookPinTarget(exe, loreBin, home string, lookPath func(string) (string, error)) (hookTarget, bool) {
	if loreBin != "" && filepath.IsAbs(loreBin) && executableHookFile(loreBin) && !strings.Contains(loreBin, shellQuoteChar) {
		// the home goes with it (devco D-423): a hook pinned to
		// LORE_HOOK_BIN under LORE_HOME=~/.lore-dev otherwise ran against
		// ~/.lore; a wrapper that sets its own LORE_HOME still wins
		if strings.Contains(home, shellQuoteChar) {
			home = ""
		}
		return hookTarget{prog: filepath.ToSlash(filepath.Clean(loreBin)), home: home}, true
	}
	if exe == "" || strings.Contains(home, shellQuoteChar) {
		return hookTarget{}, false
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if home == "" && reposync.DriverBinary(exe, lookPath) == reposync.BinaryName {
		return hookTarget{}, false // `lore` on PATH is this file: bare survives an upgrade in place
	}
	if strings.Contains(exe, shellQuoteChar) {
		return hookTarget{}, false
	}
	return hookTarget{prog: filepath.ToSlash(exe), home: home}, true
}

// line is the pin line chained first into each block.
func (t hookTarget) line() string {
	env := ""
	if t.home != "" {
		env = "LORE_HOME=" + shellQuoteChar + t.home + shellQuoteChar + " "
	}
	q := shellQuoteChar + t.prog + shellQuoteChar
	return "if [ -x " + q + " ]; then " + binaryName + "() { " + env + q + ` "$@"; }; fi ` + hookPinTag
}

// describe is the pinned program for a doctor line, with its LORE_HOME.
func (t hookTarget) describe() string {
	if t.home == "" {
		return t.prog
	}
	return t.prog + " (LORE_HOME=" + t.home + ")"
}

// runningHookPin is the pin line for this process ("" for bare `lore`).
func runningHookPin() string {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	home := os.Getenv(lorehome.EnvName)
	if home != "" {
		if abs, aerr := filepath.Abs(home); aerr == nil {
			home = abs
		}
	}
	t, ok := hookPinTarget(exe, os.Getenv(EnvHookBin), filepath.ToSlash(home), exec.LookPath)
	if !ok {
		return ""
	}
	return t.line()
}

// hookPinFor is the pin line for the hooks folder dir of the clone at top
// ("" when the folder is shared by every repo or is in the work tree, see
// the DECISION above).
func hookPinFor(top, dir string, shared bool) string {
	if shared || hooksDirInWorkTree(top, dir) {
		return ""
	}
	pin := runningHookPin()
	if pin == "" {
		// bare would do (this file is the lore on PATH, no LORE_HOME), but
		// blocks already pinned to this same file run it too: keep them,
		// so lore appearing on PATH later rewrites no hook file
		if kept, ok := keptSelfPin(dir); ok {
			return kept
		}
	}
	return pin
}

// keptSelfPin is the pin of lore's blocks in dir when it names this very
// executable with no LORE_HOME.
func keptSelfPin(dir string) (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	for _, n := range []string{hookPreCommit, hookPostCommit, hookPrePush, hookCommitMsg} {
		b, rerr := os.ReadFile(filepath.Clean(filepath.Join(dir, n)))
		if rerr != nil {
			continue
		}
		t, ok := pinnedHookProgram(string(b))
		if !ok {
			continue
		}
		if t.home == "" && sameExecutable(filepath.FromSlash(t.prog), exe) {
			return t.line(), true
		}
		return "", false
	}
	return "", false
}

// sameExecutable reports whether a and b are the same file.
func sameExecutable(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	return err == nil && os.SameFile(ia, ib)
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
	return hookTarget{prog: m[1], home: m[2]}, true
}

// executableHookFile reports whether p is a regular file someone may run.
func executableHookFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&rideExecBits != 0
}
