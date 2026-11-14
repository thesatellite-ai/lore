package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// TestHeadVersionsOneBatch (perf4): HEAD's copy of every changed file comes
// from one git batch, also for a project in a subfolder of the repo; a file
// HEAD lacks has none, and an empty list asks git nothing.
func TestHeadVersionsOneBatch(t *testing.T) {
	ctx := context.Background()
	top := t.TempDir()
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	gitT(t, top, "init", "-q")
	project := filepath.Join(top, "svc")
	data := filepath.Join(project, ".lore", "data", "tasks")
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"a.json": `{"a":1}` + "\n", "b.json": `{"b":"two"}` + "\n"}
	for n, body := range files {
		if err := os.WriteFile(filepath.Join(data, n), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, top, "add", "-A")
	gitT(t, top, "commit", "-q", "-m", "rows")
	repo, err := gitref.Open(ctx, project, gitref.Exec{})
	if err != nil {
		t.Fatal(err)
	}
	if got := headVersions(ctx, repo, project, nil); len(got) != 0 {
		t.Fatalf("no paths: %v", got)
	}
	got := headVersions(ctx, repo, project, []string{".lore/data/tasks/a.json", ".lore/data/tasks/new.json", ".lore/data/tasks/b.json"})
	if len(got) != 2 || string(got[".lore/data/tasks/a.json"]) != files["a.json"] || string(got[".lore/data/tasks/b.json"]) != files["b.json"] {
		t.Fatalf("head versions = %q", got)
	}
	// the signpost is never checked as a row (D-154): plain text passes
	note := filepath.Join(project, ".lore", "data", rowfs.SignpostFile)
	if err := os.WriteFile(note, []byte(rowfs.SignpostText), 0o600); err != nil {
		t.Fatal(err)
	}
	bad, _ := checkChangedFiles(ctx, repo, project, nil, []string{".lore/data/" + rowfs.SignpostFile})
	if len(bad) != 0 {
		t.Fatalf("the signpost was checked as a row: %v", bad)
	}
}
