package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// TestPreCommitStagesIntoTheHooksIndex: `git commit -a` and `git commit --
// <paths>` run pre-commit with GIT_INDEX_FILE naming a temporary index
// while the main index is locked. The stage step must write into that
// index (a relative value is taken from the hook's directory, the repo
// top), not fail on the locked main index (it failed with E_INTERNAL
// "pre-commit: stage .lore/data", exit 128). Without GIT_INDEX_FILE (a
// plain `git commit`) the main index is staged as before.
func TestPreCommitStagesIntoTheHooksIndex(t *testing.T) {
	cases := map[string]struct {
		index string // GIT_INDEX_FILE ("" = unset): relative to the repo top or absolute
	}{
		"plain commit":           {},
		"commit -a (relative)":   {index: ".git/next-index"},
		"commit -- p (absolute)": {index: "ABS"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLiveFixture(t)
			f.s.release()
			top := f.s.top
			row := ".lore/data/memories/mem_1.json"
			full := filepath.Join(top, filepath.FromSlash(row))
			if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(`{"a":1}`+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			gitT(t, top, "add", "-A")
			gitT(t, top, "commit", "-q", "-m", "row")
			if err := os.WriteFile(full, []byte(`{"a":2}`+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(top)
			index := filepath.Join(top, ".git", "index")
			if c.index != "" {
				tmp := c.index
				if tmp == "ABS" {
					tmp = filepath.Join(t.TempDir(), "next-index")
				}
				abs := tmp
				if !filepath.IsAbs(abs) {
					abs = filepath.Join(top, abs)
				}
				// git builds the temporary index from HEAD, as commit -a does.
				if _, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top,
					Args: []string{"read-tree", "HEAD"}, Env: []string{"GIT_INDEX_FILE=" + abs}}); err != nil {
					t.Fatal(err)
				}
				// The main index is locked while git runs the hook.
				if err := os.WriteFile(index+".lock", nil, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(index + ".lock") })
				t.Setenv("GIT_INDEX_FILE", tmp)
				index = abs
			}
			if err := f.s.h.preCommit(context.Background()); err != nil {
				t.Fatalf("pre-commit: %v", err)
			}
			out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top,
				Args: []string{"diff", "--cached", "--name-only"}, Env: []string{"GIT_INDEX_FILE=" + index}})
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(out)) != row {
				t.Fatalf("staged in %s: %q, want %s", index, out, row)
			}
		})
	}
}
