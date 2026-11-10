package dataignore

import "testing"

// TestIsOSJunk: the clutter operating systems drop into browsed folders is
// junk at any depth; lore's own files (and names merely containing a junk
// name) are not. Break-verified: dropping "._*" from OSJunkPatterns fails
// the AppleDouble cases.
func TestIsOSJunk(t *testing.T) {
	for name, want := range map[string]bool{
		".DS_Store":                      true,
		".lore/data/memories/.DS_Store":  true,
		"._foo":                          true,
		".lore/data/._mem_1.json":        true,
		"Thumbs.db":                      true,
		"desktop.ini":                    true,
		"Desktop.ini":                    true,
		".lore/data/_meta.json":          false,
		".lore/data/memories/mem_1.json": false,
		"x.DS_Store":                     false,
		"_foo":                           false,
	} {
		if got := IsOSJunk(name); got != want {
			t.Errorf("IsOSJunk(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestIsRowFile: a .json file that is not clutter.
func TestIsRowFile(t *testing.T) {
	for name, want := range map[string]bool{
		".lore/data/memories/mem_1.json": true,
		".lore/data/_meta.json":          true,
		".lore/data/._mem_1.json":        false,
		".lore/data/.DS_Store":           false,
		".lore/data/notes.txt":           false,
	} {
		if got := IsRowFile(name); got != want {
			t.Errorf("IsRowFile(%q) = %v, want %v", name, got, want)
		}
	}
}
