package toolprofile

import (
	"strings"
	"testing"
)

// TestStitchSharedPointer (devco D-23): CLAUDE.md a link to AGENTS.md is
// one file read by two tools. One block holds the Claude @import and its
// fallback line, then the Codex sentence, each once; stitching it again
// changes nothing, and the person's text stays.
// Stitching the two profiles one after the other (the old
// render) leaves only the Codex sentence and the test fails.
func TestStitchSharedPointer(t *testing.T) {
	t.Parallel()
	claude, codex := sharedBuiltin(t, NameClaudeCode), sharedBuiltin(t, NameCodex)
	link := Link{FromPointer: ".lore/LORE.md", FromRoot: ".lore/LORE.md"}
	members := []PointerMember{{Profile: codex, Link: link}, {Profile: claude, Link: link}}
	out := StitchSharedPointer("# agents\n", members)
	want := PointerStartMarker + "\n@.lore/LORE.md\n<!-- if your agent does not support @import, read .lore/LORE.md now -->\nRead `.lore/LORE.md` (at the repository root)"
	if !strings.Contains(out, want) || !strings.HasSuffix(out, "# agents\n") || strings.Count(out, PointerStartMarker) != 1 {
		t.Fatalf("shared block:\n%s", out)
	}
	if again := StitchSharedPointer(out, members); again != out {
		t.Errorf("a second stitch changed the file:\n%s", again)
	}
	if one := StitchSharedPointer("x\n", members[:1]); one != codex.StitchPointer("x\n", link) {
		t.Errorf("one member is StitchPointer: %q", one)
	}
}

// sharedBuiltin is the built-in profile named n.
func sharedBuiltin(t *testing.T, n string) Profile {
	t.Helper()
	for _, p := range Builtins() {
		if p.Name == n {
			return p
		}
	}
	t.Fatalf("no built-in profile %s", n)
	return Profile{}
}
