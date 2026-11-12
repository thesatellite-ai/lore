package toolprofile

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/mdwrap"
)

// TestBlocksNotHardWrapped: the pointer block each built-in profile stitches
// into its tool file, and the private import file render writes (after a
// person's own paragraph, with two imports), keep one physical line per
// paragraph and bullet.
func TestBlocksNotHardWrapped(t *testing.T) {
	reg, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	link := Link{FromPointer: ".lore/LORE.md", FromRoot: ".lore/LORE.md"}
	files := []string{privDir + "r1/sto_a.md", privDir + "r1/sto_b.md"}
	for _, p := range reg.Profiles() {
		texts := map[string]string{
			"pointer block":       p.PointerBlock(link),
			"stitched pointer":    p.StitchPointer("# Notes\n\nOne paragraph.\n", link),
			"private file":        p.PrivateContent("", privDir, files),
			"private after prose": p.PrivateContent("# my notes\nuse the staging db\n", privDir, files),
			"private re-run":      p.PrivateContent(p.PrivateContent("mine\n", privDir, files), privDir, files[:1]),
		}
		for name, s := range texts {
			if ps := mdwrap.Check(s); len(ps) > 0 {
				t.Errorf("%s %s is hard-wrapped:\n%s", p.Name, name, mdwrap.Lines(ps))
			}
		}
	}
}
