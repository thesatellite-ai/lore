package memory

import (
	"strings"
	"testing"
)

// TestSearchTextNoMatches is G-4: a memory search with no hits says
// "(no matches)" and points at lore search, as every other search does;
// it printed nothing, which a script could not tell from a command that
// did not run.
func TestSearchTextNoMatches(t *testing.T) {
	t.Parallel()
	got := SearchOut{Query: "zzzz-no-such-word"}.Text()
	if !strings.HasPrefix(got, "(no matches)\n") || !strings.Contains(got, "lore search --query") {
		t.Fatalf("Text() = %q, want (no matches) and the lore search hint", got)
	}
}
