package onboard

import "testing"

// TestParseGitURLLocalSpaces: a local store folder may hold spaces, typed
// or as %20 (coordinator decision, 2026-11-08); a hosted url with a space,
// and a tab or control character anywhere, is still refused.
func TestParseGitURLLocalSpaces(t *testing.T) {
	for _, c := range []struct {
		url string
		ok  bool
	}{
		{"file:///home/pat/My Stores/me.git", true},
		{"file:///home/pat/My%20Stores/me.git", true},
		{"file:///home/pat/Zoë Ünicode/store folder/me.git", true},
		{"file:///home/pat/a\tb/me.git", false},
		{"file:///home/pat/a\nb/me.git", false},
		{"https://github.com/pat/my repo.git", false},
		{"git@github.com:pat/my repo.git", false},
		{"ssh://git@host/pat/my repo.git", false},
	} {
		if _, why, ok := ParseGitURL(c.url); ok != c.ok {
			t.Errorf("ParseGitURL(%q) ok=%v (%s), want %v", c.url, ok, why, c.ok)
		}
	}
}
