package foldercase

import (
	"os"
	"path/filepath"
	"testing"
)

// caseFoldsHere reports whether the test's temp folder ignores case (the
// real file system: APFS on a Mac, ext4 on Linux).
func caseFoldsHere(t *testing.T, dir string) bool {
	t.Helper()
	p := filepath.Join(dir, "probe-a")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(dir, "PROBE-A"))
	return err == nil
}

// TestCheckerExactSpelling (devco D-414): the probe sees whether names fold
// at the repo top (through .git), and IsDir accepts a folder only as it is
// spelled on disk, on the real file system and through Folding (APFS's
// behaviour on any OS).
func TestCheckerExactSpelling(t *testing.T) {
	top := t.TempDir()
	for _, d := range []string{".git", filepath.Join("src", "Charts", "inner")} {
		if err := os.MkdirAll(filepath.Join(top, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		view  FS
		folds bool
	}{{OS{}, caseFoldsHere(t, t.TempDir())}, {Folding{}, true}} {
		c := New(tc.view, top)
		if c.Folds() != tc.folds {
			t.Errorf("%T: Folds = %v, want %v", tc.view, c.Folds(), tc.folds)
		}
		for rel, want := range map[string]bool{"src/Charts": true, "src/Charts/inner": true, "src/charts": false, "SRC/Charts/inner": false, "src/Charts/INNER": false, "src/none": false, "": true} {
			if got := c.IsDir(rel); got != want {
				t.Errorf("%T: IsDir(%q) = %v, want %v", tc.view, rel, got, want)
			}
		}
		if _, err := tc.view.Stat(filepath.Join(top, "SRC", "charts")); tc.folds != (err == nil) {
			t.Errorf("%T: Stat(SRC/charts) err = %v, folds %v", tc.view, err, tc.folds)
		}
	}
}
