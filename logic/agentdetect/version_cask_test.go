package agentdetect

import "testing"

// TestVersionFromPathHomebrew: a tool installed by Homebrew carries its
// version in the folder, for a formula (Cellar) and a cask (Caskroom: the
// shape goreleaser now writes for released tools, GR-1).
func TestVersionFromPathHomebrew(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]string{
		"/opt/homebrew/Cellar/goose/1.9.2/bin/goose":          "1.9.2",
		"/opt/homebrew/Caskroom/goose/1.9.3/goose":            "1.9.3",
		"/home/linuxbrew/.linuxbrew/Caskroom/tool/2.0.1/tool": "2.0.1",
		"/usr/local/bin/goose":                                "",
	} {
		if got := VersionFromPath(p); got != want {
			t.Errorf("VersionFromPath(%q) = %q, want %q", p, got, want)
		}
	}
}
