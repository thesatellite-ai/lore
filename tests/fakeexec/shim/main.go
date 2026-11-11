// Command shim is the Windows body of a fake program written by
// tests/fakeexec: Windows runs only .exe (and .bat/.cmd) files, never a
// "#!/bin/sh" script, so a fake named ssh is ssh.exe (a copy of this
// program) beside ssh.sh (the script) and ssh.shell (the sh to run it
// with). It runs `<sh> <name>.sh` with its own stdin, stdout, stderr and
// environment plus its arguments (in variables the script's prelude turns
// into "$@"), and exits with the script's exit code.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// shellSuffix and scriptSuffix name the files beside the shim.
const (
	scriptSuffix = ".sh"
	shellSuffix  = ".shell"
)

// parentPIDEnv is the variable holding the pid of the shim's caller
// (a fake script reads it; see main).
const parentPIDEnv = "FAKEEXEC_PARENT_PID"

// shimPIDEnv holds the pid of the nearest shim above this one. A fake
// called from another fake's script names that shim as its caller: the
// script's sh runs each program from an MSYS stub process, so the parent
// pid would name the stub, not the calling fake.
const shimPIDEnv = "FAKEEXEC_SHIM_PID"

// argCountEnv and argEnvPrefix carry the shim's arguments to the script
// (fakeexec's prelude reads them; the names are shared with it).
const (
	argCountEnv  = "FAKEEXEC_ARGC"
	argEnvPrefix = "FAKEEXEC_ARG_"
)

// exitSetup is the exit code when the shim cannot start the script.
const exitSetup = 127

func main() {
	exe, err := os.Executable()
	if err != nil {
		fail(err)
	}
	base := strings.TrimSuffix(exe, filepath.Ext(exe))
	sh, err := os.ReadFile(base + shellSuffix)
	if err != nil {
		fail(err)
	}
	// The arguments go to the script in variables (argCountEnv,
	// argEnvPrefix<i>; the script's prelude, written by fakeexec.Make,
	// turns them back into "$@"), not on sh's command line: Git for
	// Windows' sh glob-expands the unquoted words of a Windows command
	// line, and MSYS=noglob, which stopped that, also reached every
	// program the script ran (a fake git execs the real one, which runs
	// GIT_SSH_COMMAND through its own sh) and there kept the \" escapes of
	// sh -c '<cmd> "$@"', so ssh got a literal $@ instead of its host.
	cmd := exec.Command(strings.TrimSpace(string(sh)), base+scriptSuffix)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Git for Windows' sh rewrites arguments that look like POSIX paths
	// when it starts a Windows program (a fake git that execs the real
	// one): a fake passes its arguments on as it got them.
	// ParentPIDEnv names the program that ran the fake: Git for Windows'
	// sh reports $PPID as 1 for a Windows parent, so a fake that kills its
	// caller (a crash injection) reads this instead.
	caller := os.Getenv(shimPIDEnv)
	if caller == "" {
		caller = strconv.Itoa(os.Getppid())
	}
	cmd.Env = append(os.Environ(), "MSYS_NO_PATHCONV=1", "MSYS2_ARG_CONV_EXCL=*",
		parentPIDEnv+"="+caller, shimPIDEnv+"="+strconv.Itoa(os.Getpid()),
		argCountEnv+"="+strconv.Itoa(len(os.Args)-1))
	for i, a := range os.Args[1:] {
		cmd.Env = append(cmd.Env, argEnvPrefix+strconv.Itoa(i+1)+"="+a)
	}
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		os.Exit(exit.ExitCode())
	case err != nil:
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "fakeexec shim:", err)
	os.Exit(exitSetup)
}
