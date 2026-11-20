package wire

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoEndToEndTestExpectsCountlessNote (devco:D-778, bug hunt H-36):
// D-698 made every store line of a push whose commits carry no note row
// count them ("pushed (sent 1, …); pushed 1 local commit with no note rows
// (…)"); the countless syncPushedNoRows note is left only for a backend
// that gives no commit count, which no end-to-end suite runs (they all use
// git). The teame2e suite still expected the countless note and failed on
// every after-land run since D-698, unseen because the quick gate does not
// run teame2e. This guard runs in the quick gate: a test under tests/ that
// expects the countless note (any line holding it that is not a comment or
// a Never check) fails here, so the next change of that line finds every
// suite's expectation in the same landing.
func TestNoEndToEndTestExpectsCountlessNote(t *testing.T) {
	t.Parallel()
	countless := strings.TrimPrefix(syncPushedNoRows, "; ")
	countless = strings.TrimSuffix(countless, syncNoNoteRows) // "pushed local commit(s)"
	root := filepath.Join("..", "tests")
	var stale []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			code := strings.TrimSpace(line)
			if !strings.Contains(code, countless) || strings.HasPrefix(code, "//") || strings.Contains(code, "Never(") {
				continue
			}
			stale = append(stale, path+":"+strconv.Itoa(i+1))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(stale) > 0 {
		t.Fatalf("these end-to-end tests expect the countless %q note, which git-backed lore never prints since D-698; expect the counted line (\"pushed N local commit(s) with no note rows\", see storeSyncLine):\n%s", countless, strings.Join(stale, "\n"))
	}
}
