package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
)

// TestBuildTemplateWithTMPDIRGone: the empty-cache template is built when
// TMPDIR names a folder that is gone (the machine sweep's
// env_tmpdir_missing shape), in the cache's own folder, and nothing is
// left there.
func TestBuildTemplateWithTMPDIRGone(t *testing.T) {
	f := newFixture(t)
	testenv.SetTemp(t, filepath.Join(t.TempDir(), "gone"))
	img, err := buildTemplate(f.ctx, f.reg, f.exp, f.dir)
	if err != nil || len(img) == 0 {
		t.Fatalf("template with TMPDIR gone: %d bytes, %v", len(img), err)
	}
	if left, _ := filepath.Glob(filepath.Join(f.dir, "lore-cache-template-*")); len(left) > 0 {
		t.Errorf("temp folders left: %v", left)
	}
}
