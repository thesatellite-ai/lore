// Package fakeexec writes the fake programs (ssh, gh, git wrappers …) that
// tests put on a PATH or name as a binary, on every operating system.
//
// A fake is a POSIX sh script. On Unix it is written as it is, executable.
// Windows cannot run a script as a program, so there the fake is three
// files: <path>.exe (a copy of ./shim, built once per test process),
// <path>.sh (the script) and <path>.shell (the sh that runs it, Git for
// Windows' sh, found before a test narrows PATH). exec.LookPath("ssh")
// then finds ssh.exe through PATHEXT, and Write returns the path to run.
//
// Write takes syscall.ForkLock for reading while the file is open for
// writing, so no parallel fork of the test process inherits the write
// descriptor and a later exec of the fake never fails with ETXTBSY ("text
// file busy": Linux refuses to exec a file a process holds open for
// writing, and a fork between the open and the close copies the
// descriptor into the child until its own exec; forks_test.go).
package fakeexec

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// shimPkg is the import path of the Windows shim program.
const shimPkg = "github.com/thesatellite-ai/lore/tests/fakeexec/shim"

// goosWindows is runtime.GOOS on Windows.
const goosWindows = "windows"

// Write creates (or replaces) the fake program at path running script and
// returns the path to execute: path itself on Unix, path+".exe" on
// Windows. mode is the Unix mode (the Windows files are plain); a mode
// with no execute bit writes path as a plain file on every system. The test
// fails on any error, and skips on Windows when no sh is installed.
func Write(t testing.TB, path, script string, mode os.FileMode) string {
	t.Helper()
	if runtime.GOOS == goosWindows {
		if _, err := shell(); err != nil {
			t.Skipf("fake programs need a POSIX sh (Git for Windows): %v", err)
		}
	}
	run, err := Make(path, script, mode)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// Make is Write for code without a testing.TB (a TestMain): it returns
// the error instead of failing a test.
func Make(path, script string, mode os.FileMode) (string, error) {
	// a mode without an execute bit is a plain file on every system (a
	// test that needs a non-executable file named like a program)
	if runtime.GOOS != goosWindows || mode&0o111 == 0 {
		return path, writeLocked(path, []byte(script), mode)
	}
	sh, err := shell()
	if err != nil {
		return "", fmt.Errorf("fake programs need a POSIX sh (Git for Windows): %w", err)
	}
	shim, err := shimExe()
	if err != nil {
		return "", fmt.Errorf("build the fake program shim: %w", err)
	}
	for _, f := range []struct {
		name string
		body []byte
	}{{path + ".sh", []byte(withArgPrelude(script))}, {path + ".shell", []byte(sh)}} {
		if err := os.WriteFile(f.name, f.body, 0o600); err != nil {
			return "", err
		}
	}
	exe := path + ".exe"
	return exe, copyFile(shim, exe)
}

// argPrelude rebuilds "$@" from the variables the Windows shim passes the
// arguments in (FAKEEXEC_ARGC, FAKEEXEC_ARG_1 …; see shim/main.go for why
// they do not travel on sh's command line) and removes them, so a program
// the script runs does not inherit them.
const argPrelude = `if [ -n "${FAKEEXEC_ARGC+x}" ]; then
  _fe_n=$FAKEEXEC_ARGC; _fe_i=1; unset FAKEEXEC_ARGC; set --
  while [ "$_fe_i" -le "$_fe_n" ]; do
    eval "set -- \"\$@\" \"\${FAKEEXEC_ARG_$_fe_i}\""; unset "FAKEEXEC_ARG_$_fe_i"; _fe_i=$((_fe_i + 1))
  done
  unset _fe_n _fe_i
fi
`

// withArgPrelude puts argPrelude after the script's #! line (or first).
func withArgPrelude(script string) string {
	if strings.HasPrefix(script, "#!") {
		if i := strings.IndexByte(script, '\n'); i >= 0 {
			return script[:i+1] + argPrelude + script[i+1:]
		}
		return script + "\n" + argPrelude
	}
	return argPrelude + script
}

// Exe is the file name a fake program called name has on this system
// (name.exe on Windows), for a test that looks for it on disk.
func Exe(name string) string {
	if runtime.GOOS == goosWindows {
		return name + ".exe"
	}
	return name
}

func writeLocked(path string, body []byte, mode os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	if err := os.WriteFile(path, body, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode) // WriteFile applies mode only to a new file
}

var (
	shellOnce sync.Once
	shellPath string
	shellErr  error
)

// shell is the sh that runs the scripts on Windows: sh on PATH, else the
// sh.exe of the Git for Windows that installed bash (never WSL's
// System32\bash.exe, which runs Linux, not the Windows paths the tests
// pass). Found once, before any test narrows PATH.
func shell() (string, error) {
	shellOnce.Do(func() {
		if p, err := exec.LookPath("sh"); err == nil {
			shellPath = p
			return
		}
		for _, git := range []string{"git", "bash"} {
			p, err := exec.LookPath(git)
			if err != nil {
				continue
			}
			for _, rel := range []string{`..\bin\sh.exe`, `..\usr\bin\sh.exe`, `..\..\bin\sh.exe`, `..\..\usr\bin\sh.exe`} {
				c := filepath.Join(filepath.Dir(p), rel)
				if _, err := os.Stat(c); err == nil {
					shellPath = c
					return
				}
			}
		}
		shellErr = fmt.Errorf("no sh on PATH and no Git for Windows sh.exe")
	})
	return shellPath, shellErr
}

var (
	shimOnce sync.Once
	shimPath string
	shimErr  error
)

// shimExe builds ./shim once per test process into a temp folder (go
// build reuses its cache, so this is fast after the first package).
func shimExe() (string, error) {
	shimOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lore-fakeexec-")
		if err != nil {
			shimErr = err
			return
		}
		// The go tool is found on PATH (runtime.GOROOT is deprecated: the
		// build root need not exist where the test runs).
		goBin, err := exec.LookPath("go")
		if err != nil {
			shimErr = fmt.Errorf("building the fakeexec shim needs go on PATH: %w", err)
			return
		}
		_, self, _, _ := runtime.Caller(0)
		out := filepath.Join(dir, "shim.exe")
		cmd := exec.Command(goBin, "build", "-o", out, shimPkg)
		cmd.Dir = filepath.Dir(self) // inside the module, whatever the test's working folder
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			shimErr = fmt.Errorf("go build %s: %v\n%s", shimPkg, err, b)
			return
		}
		shimPath = out
	})
	return shimPath, shimErr
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
