package toolprofile

import (
	"strings"
	"testing"
)

// TestPlaceCRLFKeepsFile is devco:Q-11: a pointer file checked out with
// CRLF line ends that already holds the current block is left byte for
// byte (render used to add a blank line after the block and dirty the
// tracked file); a changed block is written back with CRLF throughout.
func TestPlaceCRLFKeepsFile(t *testing.T) {
	p := Profile{Name: "agents", PointerFile: "AGENTS.md", Import: ImportRead}
	link := Link{FromPointer: ".lore/LORE.md", FromRoot: ".lore/LORE.md"}
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	file := crlf(p.PointerBlock(link) + "\n# Team\nsee lore\n")
	if got := p.StitchPointer(file, link); got != file {
		t.Fatalf("CRLF file with the current block changed:\n%q", got)
	}
	old := crlf(PointerStartMarker + "\nold text\n" + PointerEndMarker + "\n\n# Team\n")
	got := p.StitchPointer(old, link)
	if !strings.Contains(got, "Read `.lore/LORE.md`") || strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("changed block in a CRLF file: %q", got)
	}
}
