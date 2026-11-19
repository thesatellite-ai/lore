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

// TestPlaceCRLFAndBOM is devco:D-326: a CRLF file with a byte-order mark
// gets the block in CRLF (never LF lines in a CRLF file: git warns on a
// mixed one) after the mark, which stays the file's first bytes; a second
// place changes nothing.
func TestPlaceCRLFAndBOM(t *testing.T) {
	p := Profile{Name: "agents", PointerFile: "AGENTS.md", Import: ImportRead}
	link := Link{FromPointer: ".lore/LORE.md", FromRoot: ".lore/LORE.md"}
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	in := "\ufeffBOM file\r\nwindows line\r\n"
	want := "\ufeff" + crlf(p.PointerBlock(link)+"\nBOM file\nwindows line\n")
	got := p.StitchPointer(in, link)
	if got != want {
		t.Fatalf("CRLF file with a BOM:\n%q\nwant\n%q", got, want)
	}
	if again := p.StitchPointer(got, link); again != got {
		t.Fatalf("a second place changed it:\n%q", again)
	}
}
