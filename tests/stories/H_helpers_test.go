//go:build stories && !windows

package stories

// H_helpers_test.go — small helpers of story H (recovery). Prefixed h… so
// the other stories' helper files in this package never clash with them.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// hMemID matches the first memory id a command prints.
var hMemID = regexp.MustCompile(`mem_[0-9a-f]{32}`)

// hTaskID matches the first task id a command prints.
var hTaskID = regexp.MustCompile(`tsk_[0-9a-f]{32}`)

// hFinding runs a step that guards one finding of this story (once
// waiting on it, now fixed): a subtest named after the finding id, so a
// failure names the finding it brings back and ends only this step, never
// the story around it. The regression index names the story test as that
// finding's guard.
func hFinding(t *testing.T, finding string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(finding, func(t *testing.T) {
		fn(t)
	})
}

// hRows lists the row files of one table in a project's .lore/data, sorted.
func hRows(t testing.TB, repo, table string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repo, ".lore", "data", table, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

// hID is the row id a row file is named after.
func hID(file string) string { return strings.TrimSuffix(filepath.Base(file), ".json") }

// hRel is a row file's path from the repo root, the way git and lore's
// hints name it.
func hRel(t testing.TB, repo, file string) string {
	t.Helper()
	r, err := filepath.Rel(repo, file)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// hWrite overwrites a file in place (a hand edit or a damage).
func hWrite(t testing.TB, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hRead reads a file the story wrote or lore wrote.
func hRead(t testing.TB, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// hRemoteHas reports whether any file on a remote's main holds text: a
// store remote's rows are plain JSON files, so a body is found verbatim.
func hRemoteHas(r *teamharness.Remote, text string) bool {
	for _, f := range r.Files("main") {
		if s, ok := r.Read("main", f); ok && strings.Contains(s, text) {
			return true
		}
	}
	return false
}

// hCommitRefused stages everything and asserts the commit hook refuses the
// commit saying want; the damage must never ride into history.
func hCommitRefused(t testing.TB, c *teamharness.Clone, want string) {
	t.Helper()
	c.Git("add", "-A")
	r := c.TryGit("commit", "-q", "-m", "tidy notes")
	if r.Code == 0 || !strings.Contains(r.Out(), want) {
		t.Fatalf("the commit hook let the damage through (want %q): %s", want, r)
	}
	c.Git("reset", "-q")
}
