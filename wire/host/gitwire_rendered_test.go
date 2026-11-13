package host

import (
	"strings"
	"testing"
)

// TestGitAttributesKeepOursEveryRenderedFile (devco D-19): .lore/LORE.full.md
// had no rule, so two people adding rules and pulling got a text conflict
// in it while LORE.md merged through lore-keep-ours. Every committed
// rendered file now has the keep-ours rule.
// Without the LORE.full.md line the test fails.
func TestGitAttributesKeepOursEveryRenderedFile(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{".lore/LORE.md", ".lore/LORE.full.md", ".claude/rules/lore/lore-*.md", ".cursor/rules/lore-*.mdc"} {
		want := rel + " text eol=lf merge=" + mergeDriverKeepOurs + " linguist-generated=true"
		found := false
		for _, l := range gitAttributeLines {
			found = found || strings.TrimSpace(l) == want
		}
		if !found {
			t.Errorf(".gitattributes block lacks %q:\n%s", want, strings.Join(gitAttributeLines, "\n"))
		}
	}
}
