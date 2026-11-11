package fakeexec

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestFakeRunsByPathAndByName: a fake runs through the path Write returns
// and through a PATH lookup of its bare name, gets its arguments (spaces
// and backslashes intact), its stdin and its environment, and exits with
// the script's code. On Windows this is the shim; on Unix the script.
func TestFakeRunsByPathAndByName(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nread line\nprintf '%s|%s|%s|%s\\n' \"$1\" \"$2\" \"$line\" \"$FAKEEXEC_PROBE\"\nexit 3\n"
	run := Write(t, filepath.Join(dir, "probe-tool"), script, 0o700)
	if _, err := os.Stat(filepath.Join(dir, Exe("probe-tool"))); err != nil {
		t.Fatalf("Exe names a file that is not there: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	byName, err := exec.LookPath("probe-tool")
	if err != nil {
		t.Fatalf("PATH lookup: %v", err)
	}
	for _, bin := range []string{run, byName} {
		cmd := exec.Command(bin, "a b", `C:\x\y`)
		cmd.Stdin = strings.NewReader("from stdin\n")
		cmd.Env = append(os.Environ(), "FAKEEXEC_PROBE=env ok")
		out, err := cmd.Output()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 3 {
			t.Fatalf("%s: err = %v, want exit 3", bin, err)
		}
		if got, want := strings.TrimSpace(string(out)), `a b|C:\x\y|from stdin|env ok`; got != want {
			t.Fatalf("%s: output %q, want %q", bin, got, want)
		}
	}
}

// TestFakeKeepsGlobAndBraceArguments: a refspec or a revision with a
// glob or braces (refs/heads/*, HEAD^{tree}) reaches the script as given,
// even where the glob matches files in the working folder. Before, Git
// for Windows' sh expanded the unquoted words of its command line.
func TestFakeKeepsGlobAndBraceArguments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "match-me"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run := Write(t, filepath.Join(dir, "args-tool"), "#!/bin/sh\nprintf '%s|' \"$@\"\n", 0o700)
	args := []string{"match-*", "HEAD^{tree}", "+refs/heads/*:refs/remotes/origin/*", "{a,b}"}
	cmd := exec.Command(run, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), strings.Join(args, "|")+"|"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// TestArgPreludeRebuildsArguments: the prelude a Windows fake starts with
// turns FAKEEXEC_ARGC / FAKEEXEC_ARG_<i> back into "$@" (quotes,
// backslashes, $, globs, empty words and spaces as given) and leaves none
// of the variables to the programs the script runs. Run with any sh, so
// it holds on every system.
func TestArgPreludeRebuildsArguments(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh here: the prelude is a sh script")
	}
	script := withArgPrelude("#!/bin/sh\nprintf '%s|' \"$#\" \"$@\"\nenv | grep -c '^FAKEEXEC_ARG' || true\n")
	f := filepath.Join(t.TempDir(), "s.sh")
	if err := os.WriteFile(f, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{`say "hi"`, `a\b\`, "$@", "", "x y", "refs/*", "HEAD^{tree}"}
	cmd := exec.Command(sh, f)
	cmd.Env = append(os.Environ(), "FAKEEXEC_ARGC="+strconv.Itoa(len(args)))
	for i, a := range args {
		cmd.Env = append(cmd.Env, "FAKEEXEC_ARG_"+strconv.Itoa(i+1)+"="+a)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), strconv.Itoa(len(args))+"|"+strings.Join(args, "|")+"|0\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// TestFakeChildShellKeepsQuotes: a program a fake runs passes quoted
// words to its own sh as usual (a fake git execs the real one, which runs
// GIT_SSH_COMMAND as sh -c '<cmd> "$@"'). Before, on Windows the shim's
// MSYS=noglob reached that sh, which then kept the \" escapes of its
// command line: ssh got a literal $@ instead of the host and command.
func TestFakeChildShellKeepsQuotes(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git here")
	}
	dir := t.TempDir()
	run := Write(t, filepath.Join(dir, "outer"), "#!/bin/sh\nexec \"$FAKEEXEC_TEST_GIT\" -c 'alias.p=!printf \"<%s>\" \"$@\"; :' p \"$@\"\n", 0o700)
	cmd := exec.Command(run, "one", "t w o", `th"ree`)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FAKEEXEC_TEST_GIT="+git, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(dir, "none"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got, want := string(out), `<one><t w o><th"ree>`; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
