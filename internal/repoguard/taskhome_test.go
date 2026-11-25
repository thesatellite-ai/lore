package repoguard

// A Taskfile task that runs the lore binary itself (not through a test
// harness or tests/lib, which give every run its own HOME) must give it a
// temp HOME: `task bench` ran lore in a temp repo with the person's own
// home, so each run left a cache in ~/.lore/cache that outlived the repo
// (public issue #178, devco:D-990), and any lore command there now also
// starts the daily cache sweep of that home.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// taskHeader is a task's first line in a Taskfile (two-space indent).
var taskHeader = regexp.MustCompile(`(?m)^  [A-Za-z0-9:_-]+:\s*$`)

// loreRun is a direct run of the built binary: the binary followed by a
// command word (not `-o <bin> ./cmd/lore`, `cp <bin> …` or `VAR=<bin>`).
var loreRun = regexp.MustCompile(`(^|[\s;&|(])("?\$LORE"?|\{\{\.LORE_BIN\}\}|\{\{\.BENCH_BIN\}\})[ \t]+[a-z]`)

func TestTaskfileLoreRunsUseATempHome(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Taskfile.yml", "Taskfile.lore.yml"} {
		raw, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		starts := taskHeader.FindAllStringIndex(text, -1)
		for i, s := range starts {
			end := len(text)
			if i+1 < len(starts) {
				end = starts[i+1][0]
			}
			block := text[s[0]:end]
			if !loreRun.MatchString(block) {
				continue
			}
			if !strings.Contains(block, `HOME="`) {
				t.Errorf("%s task %s runs the lore binary with the person's own HOME: export HOME (or LORE_HOME) to a temp folder in the same command", name, strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text[s[0]:s[1]]), ":")))
			}
		}
	}
}
