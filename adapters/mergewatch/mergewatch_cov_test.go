package mergewatch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ExecRunner runs a real program in dir, returns stdout only, and on
// failure names the command and carries the program's stderr.
func TestExecRunner(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	out, err := ExecRunner(ctx, dir, "sh", "-c", ": > ran-here; pwd; echo noise >&2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) == "" || strings.Contains(string(out), "noise") {
		t.Fatalf("stdout only: %q", out)
	}
	// a file made in the working folder, not pwd's text: Git for
	// Windows' sh prints its own /tmp/... spelling of a Windows folder
	if _, serr := os.Stat(filepath.Join(dir, "ran-here")); serr != nil {
		t.Fatalf("runs in dir %q: %q (%v)", dir, out, serr)
	}
	_, err = ExecRunner(ctx, dir, "sh", "-c", "echo 'gh: not logged in' >&2; exit 4")
	var ee *exec.ExitError
	if err == nil || !errors.As(err, &ee) || ee.ExitCode() != 4 {
		t.Fatalf("exit error kept: %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "sh -c") || !strings.Contains(msg, "gh: not logged in") {
		t.Fatalf("message names the command and stderr: %q", msg)
	}
	_, err = ExecRunner(ctx, dir, "lore-no-such-program-xyz")
	if err == nil || !strings.Contains(err.Error(), "lore-no-such-program-xyz") || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("a missing program is a clear error: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ExecRunner(cctx, dir, "sh", "-c", "sleep 5"); err == nil {
		t.Fatal("a cancelled context stops the program")
	}
}
