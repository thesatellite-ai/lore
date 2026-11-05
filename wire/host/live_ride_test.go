package host

import (
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
				if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := ensureHookBlock(dir, hookPrePush, rideHookMarker, rideHookLines[hookPrePush]); err != nil {
					t.Fatal(err)
				}
			}
			b, _ := os.ReadFile(path)
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
