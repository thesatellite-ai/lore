package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// devco D-597: org link in a client repo installs lore's leak guard hook
// in .git/hooks/commit-msg and then ended "nothing was written to this
// repo". The closing line now says no tracked file was written and names
// the hook when the clone has it.
func TestOrgLinkClosingLineNamesHookD597(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	ctx := context.Background()
	if got := repoUntouchedLine(ctx, dir); strings.Contains(got, "nothing was written") || strings.Contains(got, "hook") {
		t.Errorf("no hook: %q, want only that no tracked file was written", got)
	}
	hook := filepath.Join(dir, ".git", "hooks", "commit-msg")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nlore "+leakCheckHookLine+" \"$1\"\n"), 0o755); err != nil { //nolint:gosec // a hook must be executable
		t.Fatal(err)
	}
	got := repoUntouchedLine(ctx, dir)
	if strings.Contains(got, "nothing was written") || !strings.Contains(got, "no tracked file of this repo was written") || !strings.Contains(got, "leak guard hook (commit-msg)") {
		t.Errorf("with the leak guard hook: %q, want no tracked file written and the hook named", got)
	}
}
