package wire

import (
	"strings"
	"testing"
)

// TestClientInitWritesNamesGitignore (devco:N-10): lore init's refusal in
// a client repo names every tracked file init changes, the .gitignore
// block included (it was appended but not named).
func TestClientInitWritesNamesGitignore(t *testing.T) {
	for _, want := range []string{".lore/", ".gitattributes", ".gitignore", "CLAUDE.md/AGENTS.md", "git hooks"} {
		if !strings.Contains(clientInitWrites, want) {
			t.Errorf("clientInitWrites does not name %s: %q", want, clientInitWrites)
		}
	}
}
