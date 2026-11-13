package git

import (
	"path/filepath"
	"testing"
)

// TestAtOrAboveHome pins B-7's hint: the home folder and its parents are
// never told to `git init`; a folder under home still is.
func TestAtOrAboveHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cases := []struct {
		dir  string
		want bool
	}{
		{home, true},
		{filepath.Dir(home), true},
		{filepath.Join(home, "notes"), false},
		{"", false},
	}
	for _, c := range cases {
		if got := atOrAboveHome(c.dir, home); got != c.want {
			t.Errorf("atOrAboveHome(%q, home) = %v, want %v", c.dir, got, c.want)
		}
	}
}
