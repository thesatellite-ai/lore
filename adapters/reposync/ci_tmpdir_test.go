package reposync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
)

// TestMergeConfigWithTMPDIRGone: the merge's attributes file is written
// in the repo's git folder when TMPDIR names a folder that is gone, and
// its cleanup removes it.
func TestMergeConfigWithTMPDIRGone(t *testing.T) {
	near := t.TempDir()
	testenv.SetTemp(t, filepath.Join(t.TempDir(), "gone"))
	cfg, cleanup, err := mergeConfig("", "", near)
	if err != nil || len(cfg) == 0 {
		t.Fatalf("merge config with TMPDIR gone: %v %v", cfg, err)
	}
	left, _ := filepath.Glob(filepath.Join(near, "lore-ci-merge-attributes-*"))
	if len(left) != 1 {
		t.Fatalf("attributes file not in the fallback folder: %v", left)
	}
	cleanup()
	if _, err := os.Stat(left[0]); !os.IsNotExist(err) {
		t.Errorf("cleanup left %s", left[0])
	}
}
