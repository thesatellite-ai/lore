// testexec_test.go — how the CLI tests run external programs (git, sh, go,
// the e2e lore binary) and write the stand-in scripts they exec. A program
// runs from the absolute path exec.LookPath resolved, one argv entry per
// argument, so no shell parses a variable argument: the same shape as
// program_cmd.go and the root module's wire/gitexec.go (P2-LINT-4).
package main

import (
	"io/fs"
	"os/exec"
	"testing"
)

// testExecMode is the mode of a stand-in script a test writes (a git hook, a
// fake gh/task/claude on PATH): owner read/write/exec, nothing for anyone
// else, the strictest mode that still lets git and the shell exec it.
const testExecMode fs.FileMode = 0o700

// lookedUpCmd prepares name (looked up on PATH; a path containing a
// separator is checked as is) with args. It is for callers without a
// testing.TB, such as TestMain.
func lookedUpCmd(name string, args ...string) (*exec.Cmd, error) {
	bin, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	return &exec.Cmd{Path: bin, Args: append([]string{bin}, args...)}, nil
}

// testCmd is lookedUpCmd for a test: a program that is not installed fails
// the test at once, naming the program.
func testCmd(t testing.TB, name string, args ...string) *exec.Cmd {
	t.Helper()
	cmd, err := lookedUpCmd(name, args...)
	if err != nil {
		t.Fatalf("%s is required for this test: %v", name, err)
	}
	return cmd
}
