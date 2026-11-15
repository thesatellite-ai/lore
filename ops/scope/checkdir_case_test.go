package scope

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/foldercase"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCheckDirExactSpelling (devco D-414): on a case-insensitive file system
// (foldercase.Folding stands in for APFS on any OS) `lore scope add
// dir:SRC/charts` found the folder src/charts and stored a scope path no
// file path matches. A folder scope must name the folder as it is spelled
// on disk; the right spelling still passes.
func TestCheckDirExactSpelling(t *testing.T) {
	top := t.TempDir()
	if err := os.MkdirAll(filepath.Join(top, "src", "charts"), 0o750); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", top).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	for _, view := range []foldercase.FS{foldercase.OS{}, foldercase.Folding{}} {
		dirs := foldercase.New(view, top)
		if err := checkDirIn(dirs, top, "src/charts"); err != nil {
			t.Errorf("%T: src/charts refused: %v", view, err)
		}
		for _, bad := range []string{"SRC/charts", "src/Charts", "src/nope"} {
			err := checkDirIn(dirs, top, bad)
			if !errs.Has(err, errs.InvalidInput) {
				t.Errorf("%T: %s = %v, want an InvalidInput refusal", view, bad, err)
			}
		}
	}
}
