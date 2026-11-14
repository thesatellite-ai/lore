package host

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire"
)

// TestWithFileBlockCRLFAndOverride is devco:Q-11: a .gitattributes that
// git checked out with CRLF (a team `eol=crlf` rule applies to it too)
// with lore's current block is left byte for byte (it used to gain a
// blank line on every write and stay "not set up"); a team catch-all
// after the block moves lore's block to the end, keeping CRLF, and the
// result is stable.
func TestWithFileBlockCRLFAndOverride(t *testing.T) {
	block := "# >>> " + gitBlockMarker + " >>>\n" + strings.Join(gitAttributeLines, "\n") + "\n# <<< " + gitBlockMarker + " <<<\n"
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

	current := crlf(block + "\n*.png binary\n")
	if got := withFileBlock(current, gitBlockMarker, gitAttributeLines); got != current {
		t.Fatalf("CRLF file with the current block was rewritten:\n%q", got)
	}

	team := crlf(block + "\n# windows team\n* text=auto eol=crlf\n")
	got := withFileBlock(team, gitBlockMarker, gitAttributeLines)
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("the moved file mixes LF into CRLF:\n%q", got)
	}
	if _, over := wire.AttrBlockOverride(got, gitBlockMarker); over {
		t.Fatalf("lore's block is still overridden after the write:\n%s", got)
	}
	if !strings.HasPrefix(got, "# windows team\r\n* text=auto eol=crlf\r\n\r\n# >>> ") || !strings.HasSuffix(got, "# <<< "+gitBlockMarker+" <<<\r\n") {
		t.Fatalf("lore's block not moved after the team's line:\n%q", got)
	}
	if again := withFileBlock(got, gitBlockMarker, gitAttributeLines); again != got {
		t.Fatalf("a second write changed the moved file:\n%q", again)
	}
}
