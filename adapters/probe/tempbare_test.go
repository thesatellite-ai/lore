package probe

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTempBareFolderRemovedMeanwhile: another lore process's home tidy
// removes the empty probe folder right after this probe made it; the
// probe makes it again instead of failing "cannot create a temp repo".
func TestTempBareFolderRemovedMeanwhile(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "tmp")
	removed := 0
	mkdirTemp = func(dir, pattern string) (string, error) {
		if removed == 0 {
			removed++
			if err := os.Remove(dir); err != nil {
				t.Fatalf("remove the probe folder: %v", err)
			}
		}
		return os.MkdirTemp(dir, pattern)
	}
	t.Cleanup(func() { mkdirTemp = os.MkdirTemp })

	p := New(Options{TmpDir: tmp})
	dir, o := p.tempBare(ctxT(t))
	if o != nil {
		t.Fatalf("temp repo after the folder was removed: %+v", *o)
	}
	if removed != 1 {
		t.Fatalf("the folder was removed %d times, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		t.Fatalf("no bare repo at %s: %v", dir, err)
	}
}
