package gitref

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ZeroOID matches the repository's hash: 40 zeros for sha1, 64 for sha256,
// so a compare-and-swap "must not exist" is right in both kinds of repo.
func TestZeroOIDPerObjectFormat(t *testing.T) {
	ctx := context.Background()
	for format, n := range map[string]int{"sha1": 40, "sha256": 64} {
		t.Run(format, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "r")
			if out, err := exec.Command("git", "init", "-q", "--bare", "--object-format="+format, dir).CombinedOutput(); err != nil {
				t.Skipf("git cannot init %s: %s", format, out)
			}
			r, err := Open(ctx, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			z := r.ZeroOID()
			if len(z) != n || strings.Trim(z, "0") != "" {
				t.Fatalf("ZeroOID = %q, want %d zeros", z, n)
			}
		})
	}
}

// Run always runs in the repository's folder, whatever Dir the caller set,
// and returns git's output untouched (not trimmed).
func TestRunUsesRepoDirAndKeepsOutput(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "r")
	if out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	r, err := Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(ctx, Invocation{Dir: t.TempDir(), Args: []string{"rev-parse", "--is-bare-repository"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "true\n" {
		t.Fatalf("Run output %q, want untouched \"true\\n\" from the repo dir", out)
	}
	_, err = r.Run(ctx, Invocation{Args: []string{"no-such-command"}})
	var ge *Error
	if !errors.As(err, &ge) {
		t.Fatalf("a failing git is a *Error: %v", err)
	}
	if code, ok := ExitCode(err); !ok || code == 0 {
		t.Fatalf("exit code: %d %v", code, ok)
	}
	msg := err.Error()
	if !strings.Contains(msg, "git no-such-command") || !strings.Contains(msg, ge.Stderr) || ge.Stderr == "" {
		t.Fatalf("message names the command and git's stderr: %q", msg)
	}
}

func TestErrorMessages(t *testing.T) {
	inner := errors.New("exit status 128")
	e := &Error{Args: []string{"push", "origin", "main"}, ExitCode: 128, Stderr: "fatal: denied", Err: inner}
	if got := e.Error(); got != "git push origin main: exit status 128: fatal: denied" {
		t.Fatalf("Error() = %q", got)
	}
	if !errors.Is(e, inner) {
		t.Fatal("Unwrap exposes the exec error")
	}
	if _, ok := ExitCode(&Error{ExitCode: noExitCode}); ok {
		t.Fatal("a git that never ran has no exit code")
	}
	cases := []struct {
		err  CASError
		want string
	}{
		{CASError{Ref: "refs/lore/live", Expected: "abc", Actual: "def"}, "gitref: refs/lore/live moved: expected abc, found def"},
		{CASError{Ref: "refs/lore/live", Expected: "", Actual: "def"}, "gitref: refs/lore/live moved: expected (missing), found def"},
		{CASError{Ref: "refs/lore/live", Expected: "abc", Actual: ""}, "gitref: refs/lore/live moved: expected abc, found (missing)"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("CASError = %q, want %q", got, tc.want)
		}
		if !errors.Is(&tc.err, ErrRefMoved) {
			t.Error("a CAS loss is ErrRefMoved")
		}
	}
}
