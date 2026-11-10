package host

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ensureHookBlock chains the block into an existing script right after its
// shebang, keeps the user's lines, replaces its own block in place on a
// second run (never twice), and makes the script executable.
func TestEnsureHookBlock(t *testing.T) {
	cases := []struct{ name, before string }{
		{"no script", ""},
		{"user script", "#!/bin/sh\necho mine\nexit 0\n"},
		{"no shebang", "echo mine\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, hookPrePush)
			if tc.before != "" {
				if err := os.WriteFile(path, []byte(tc.before), 0o600); err != nil { // no exec bit: ensureHookBlock must add it
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := ensureHookBlock(dir, hookPrePush, rideHookMarker, rideHookLines[hookPrePush]); err != nil {
					t.Fatal(err)
				}
			}
			b, _ := os.ReadFile(filepath.Clean(path))
			got := string(b)
			if n := strings.Count(got, "# >>> "+rideHookMarker+" >>>"); n != 1 {
				t.Fatalf("one block expected, got %d:\n%s", n, got)
			}
			if !strings.Contains(got, "lore sync hook pre-push") || (tc.before != "" && !strings.Contains(got, "echo mine")) {
				t.Fatalf("block and user lines must both be there:\n%s", got)
			}
			if tc.before != "" && strings.Index(got, "lore sync hook") > strings.Index(got, "echo mine") && strings.HasPrefix(tc.before, "#!") {
				t.Fatalf("the block goes before the user's lines (an exit 0 must not skip it):\n%s", got)
			}
			if fi, _ := os.Stat(path); fi.Mode().Perm()&rideExecBits == 0 {
				t.Fatalf("the hook must be executable: %v", fi.Mode())
			}
		})
	}
}

// A hook that is running while lore re-wires it keeps reading its old
// bytes: sh reads a script from an offset as it goes, so a rewrite in place
// under the commit-msg hook (which runs lore) failed the first commit after
// an upgrade from 0.1.17 with `Syntax error: ")" unexpected`. The new
// script replaces the file instead; a hook manager's symlink stays a link.
func TestEnsureHookBlockRunningScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, hookCommitMsg)
	old := "#!/bin/sh\n# >>> lore-sync >>>\nold line\n# <<< lore-sync <<<\n"
	if err := os.WriteFile(path, []byte(old), 0o750); err != nil {
		t.Fatal(err)
	}
	running, err := os.Open(filepath.Clean(path)) // the sh that runs the hook
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = running.Close() }()
	if err := ensureHookBlock(dir, hookCommitMsg, "lore-sync", []string{"a new and much longer line than before"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(running); string(b) != old {
		t.Fatalf("the running hook must read on its old script, read:\n%s", b)
	}
	if b, _ := os.ReadFile(filepath.Clean(path)); !strings.Contains(string(b), "much longer line") {
		t.Fatalf("the hook holds the new block:\n%s", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o750 {
		t.Fatalf("the hook keeps its permissions 0750, has %v", fi.Mode().Perm())
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*"+hookTmpSuffix+"*")); len(m) != 0 {
		t.Fatalf("no temp file is left: %v", m)
	}

	// A symlinked hook is written through and stays a link.
	target := filepath.Join(dir, "managed-pre-push")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho managed\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, hookPrePush)
	if err := os.Symlink("managed-pre-push", link); err != nil {
		t.Fatal(err)
	}
	if err := ensureHookBlock(dir, hookPrePush, rideHookMarker, rideHookLines[hookPrePush]); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlinked hook must stay a link: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Clean(target)); !strings.Contains(string(b), "echo managed") || !strings.Contains(string(b), rideHookMarker) {
		t.Fatalf("the link's target holds both the managed lines and lore's block:\n%s", b)
	}
}
