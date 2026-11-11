package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
)

// TestSnapshotWithTMPDIRGone: a migration snapshot of the working files
// is taken when TMPDIR names a folder that is gone (its temp index goes
// to the git folder), and no temp folder is left there.
func TestSnapshotWithTMPDIRGone(t *testing.T) {
	work := t.TempDir()
	c := exec.Command("git", "init", "-q")
	c.Dir = work
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(work, ".lore", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".lore", "data", "x.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	testenv.SetTemp(t, filepath.Join(t.TempDir(), "gone"))
	tree, err := s.Snapshot(context.Background(), work, work, ".lore/data")
	if err != nil || tree == "" {
		t.Fatalf("snapshot with TMPDIR gone: %q %v", tree, err)
	}
	if left, _ := filepath.Glob(filepath.Join(work, ".git", "lore-migrate-index-*")); len(left) > 0 {
		t.Errorf("temp folders left: %v", left)
	}
}
