package skills

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/mdwrap"
)

// TestBundleNotHardWrapped: every page `lore skill install` writes keeps
// one physical line per paragraph, bullet and table cell (front matter and
// code blocks aside).
func TestBundleNotHardWrapped(t *testing.T) {
	files, names, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("empty bundle")
	}
	for _, n := range names {
		if ps := mdwrap.Check(string(files[n])); len(ps) > 0 {
			t.Errorf("%s is hard-wrapped:\n%s", n, mdwrap.Lines(ps))
		}
	}
}
