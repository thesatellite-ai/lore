package git

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// main_test.go — the fake executables the package's tests run, written once
// before any test starts.
//
// Why once, and before m.Run: Linux refuses to exec a file that any process
// holds open for writing (ETXTBSY, "text file busy"). os.WriteFile's fd is
// close-on-exec, but a parallel test that forks git between the open and the
// close hands the fd to its child until that child execs, and an exec of the
// fake in that window fails (golang/go#22315). Writing one per test while
// other tests ran flaked TestFetchScopeReusesTheOpenFetch under load. Written
// and closed here, no write fd of an executable exists while tests fork; the
// per-test part (where to log, what to answer) reaches each fake through the
// clone's own environment (WithRoute), never the process environment, so
// parallel tests stay apart.

// gitLogEnv names the file the logging git wrapper appends each call's
// arguments to; unset, the wrapper only runs the real git.
const gitLogEnv = "LORE_TEST_GIT_LOG"

// sshStderrEnv is the line the refusing fake ssh prints to stderr before it
// fails the way ssh does (exit 255).
const sshStderrEnv = "LORE_TEST_SSH_STDERR"

// fakeExecMode is the mode of the fake executables: run by this user only.
const fakeExecMode = 0o700

var (
	// loggingGitBin logs each git call (gitLogEnv) and runs the real git.
	loggingGitBin string
	// refusingSSHBin answers every connection with sshStderrEnv and exit 255.
	refusingSSHBin string
)

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

// runMain writes the fakes into a package temp folder, runs the tests and
// removes the folder (a deferred call would not run past os.Exit, hence the
// separate function).
func runMain(m *testing.M) int {
	real, err := lookGit(DefaultBinary)
	if err != nil {
		fmt.Fprintln(os.Stderr, "git backend tests: find git:", err)
		return 1
	}
	dir, err := os.MkdirTemp("", "lore-gitbackend-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "git backend tests:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	loggingGitBin = filepath.Join(dir, "git")
	refusingSSHBin = filepath.Join(dir, "ssh")
	fakes := map[string]string{
		loggingGitBin:  "#!/bin/sh\n[ -n \"$" + gitLogEnv + "\" ] && echo \"$*\" >> \"$" + gitLogEnv + "\"\nexec '" + real + "' \"$@\"\n",
		refusingSSHBin: "#!/bin/sh\nprintf '%s\\n' \"$" + sshStderrEnv + "\" >&2\nexit 255\n",
	}
	for path, script := range fakes {
		if err := os.WriteFile(path, []byte(script), fakeExecMode); err != nil {
			fmt.Fprintln(os.Stderr, "git backend tests:", err)
			return 1
		}
	}
	return m.Run()
}
