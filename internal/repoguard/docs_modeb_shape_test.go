package repoguard

// The shared-database docs (docs/sync/shared-database-mode-b.md Step 4,
// docs/reference/environment.md, docs/operations/secrets-and-pii.md) paste
// the output of `lore fix` on a repo still on the retired v0.1.17 shared
// database. devco:D-855 changed that conversion: rows land in the project's
// folder of the new store and stores.yaml attaches the store at that folder,
// so every printed step names the folder. The transcripts were not re-run
// with the fix (the docs examples run only in the full gate), and the pages
// kept showing the old steps that left a repo with no project (devco:D-863).
// This test reads the transcripts and the pages, needs no binary, and fails
// on any mode-b step in the old shape, so a page cannot teach the broken
// conversion again.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// retiredModeBSteps are the step lines the pre-D-855 conversion printed: a
// row moved into the store's root ("→ store shared (path)", no folder) and
// the store attached whole ("add store shared file://…" with no folder).
var retiredModeBSteps = []*regexp.Regexp{
	regexp.MustCompile(`mode-b: \S+ → store \S+ \(`),
	regexp.MustCompile(`mode-b: \.lore/stores\.yaml: add store \S+ \S+$`),
}

func TestDocsModeBExamplesShowProjectFolder(t *testing.T) {
	t.Parallel()
	var files []string
	for _, root := range []string{"docs", "tests/docs/transcripts", "tests/team/docs/transcripts"} {
		dir := filepath.Join(repoRoot, filepath.FromSlash(root))
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".txt")) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no docs pages or transcripts found: the walk roots moved")
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, re := range retiredModeBSteps {
				if re.MatchString(strings.TrimSpace(line)) {
					t.Errorf("%s:%d shows the retired mode-b step %q (rows into the store root, no project folder): re-run its docs section (task lore:docs:examples:core -- <section>) and paste", path, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
