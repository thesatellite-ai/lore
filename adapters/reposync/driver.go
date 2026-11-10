package reposync

// driver.go — what a clone's `merge.lore*.driver` config value means, and
// which program name lore writes into it. Shared by the per-clone git
// wiring (wire/host/gitwire.go), which decides whether to write or keep a
// value, and `lore doctor` (wire/hostbase_doctor_sync.go), which reports a
// value that cannot run.
//
// A value is a lore driver when it reads `<program> merge-driver …`, where
// <program> is a bare name (found on PATH when git runs the merge) or a
// single-quoted absolute path (what lore writes when the running binary is
// not the `lore` on PATH). Only an absolute path can be checked here: a
// bare name depends on the PATH of whatever runs the merge (a terminal, an
// editor, a GUI client), so it is never judged missing.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// driverSubcommand is the hidden command git's driver line calls
// (wire/host/mergedriver.go mergeDriverUse).
const driverSubcommand = "merge-driver"

// shellQuote is the quote lore wraps an absolute driver path in.
const shellQuote = "'"

// DriverProgram is the program a merge driver value runs and whether the
// value is a lore merge driver at all (`<program> merge-driver …`). The
// program comes back without its quotes.
func DriverProgram(value string) (string, bool) {
	v := strings.TrimSpace(value)
	var prog, rest string
	if strings.HasPrefix(v, shellQuote) {
		end := strings.Index(v[1:], shellQuote)
		if end < 0 {
			return "", false
		}
		prog, rest = v[1:1+end], v[2+end:]
	} else {
		var ok bool
		if prog, rest, ok = strings.Cut(v, " "); !ok {
			return "", false
		}
	}
	if prog == "" {
		return "", false
	}
	if next, _, _ := strings.Cut(strings.TrimSpace(rest), " "); next != driverSubcommand {
		return "", false
	}
	return prog, true
}

// DriverMissing reports whether value is a lore merge driver whose program
// is an absolute path that no longer holds an executable file (a deleted
// scratch build, a package-manager directory removed by an upgrade): every
// merge through it fails. A bare program name is never missing (see the
// file comment).
func DriverMissing(value string) bool {
	prog, ok := DriverProgram(value)
	if !ok || !filepath.IsAbs(filepath.FromSlash(prog)) {
		return false
	}
	return !executableFile(filepath.FromSlash(prog))
}

// DriverRuns reports whether value is a lore merge driver that can run: a
// lore driver line whose program is not a missing absolute path.
func DriverRuns(value string) bool {
	_, ok := DriverProgram(value)
	return ok && !DriverMissing(value)
}

// DriverBinary is the program lore writes into a clone's driver config for
// the running executable exe: the bare BinaryName when `lore` on PATH is
// that same file (so the config survives the binary moving, e.g. a
// package-manager upgrade, as long as PATH still finds it), else exe's
// absolute path single-quoted for git's shell (a scratch build or a copy
// outside PATH still merges with the binary that wrote the rows). A path
// holding a quote, or no exe at all, falls back to BinaryName.
//
// DECISION (mergedriver batch): bare only for the SAME file, not for any
// lore on PATH, so a merge never falls to an older lore on PATH that
// cannot read `_v: 2` rows.
func DriverBinary(exe string, lookPath func(string) (string, error)) string {
	if exe == "" {
		return BinaryName
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if onPath, err := lookPath(BinaryName); err == nil && sameFile(onPath, exe) {
		return BinaryName
	}
	if strings.Contains(exe, shellQuote) {
		return BinaryName
	}
	return shellQuote + filepath.ToSlash(exe) + shellQuote
}

// RunningDriverBinary is DriverBinary for this process, looking `lore` up
// on this process's PATH.
func RunningDriverBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return BinaryName
	}
	return DriverBinary(exe, exec.LookPath)
}

// sameFile reports whether a and b are the same file (after symlinks).
func sameFile(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ia, ib)
}

// execBits is any of the owner, group or other execute permission bits.
const execBits = 0o111

// executableFile reports whether p is a regular file someone may execute
// (on Windows, which has no execute bits, any regular file).
func executableFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || fi.Mode().Perm()&execBits != 0
}
