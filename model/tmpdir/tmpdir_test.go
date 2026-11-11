package tmpdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
)

// TestFallbackWhenTMPDIRGone: with TMPDIR naming a folder that is gone,
// Dir and File land in the first usable fallback; with TMPDIR fine they
// use it; with no usable place they fail.
func TestFallbackWhenTMPDIRGone(t *testing.T) {
	good := t.TempDir()
	fb := t.TempDir()
	testenv.SetTemp(t, good)
	if d, err := Dir("x-", fb); err != nil || !strings.HasPrefix(d, good) {
		t.Fatalf("TMPDIR fine: %q %v", d, err)
	}
	testenv.SetTemp(t, filepath.Join(good, "gone"))
	d, err := Dir("x-", "", filepath.Join(good, "also-gone"), fb)
	if err != nil || filepath.Dir(d) != fb {
		t.Fatalf("Dir with TMPDIR gone: %q %v", d, err)
	}
	f, err := File("y-*", fb)
	if err != nil || filepath.Dir(f.Name()) != fb {
		t.Fatalf("File with TMPDIR gone: %v %v", f, err)
	}
	_ = f.Close()
	if _, err := Dir("x-"); err == nil {
		t.Fatal("no usable place, yet Dir worked")
	}
	if _, err := os.Stat(fb); err != nil {
		t.Fatal(err)
	}
}
