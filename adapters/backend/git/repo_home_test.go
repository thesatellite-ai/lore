package git

import (
	"path/filepath"
	"strings"
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

// TestHomeHintNamesStoreAndSetup pins devco D-520: a write refused in the
// home folder (not a work tree) names the store (--to, --store) and the
// setup (--setup) a person can name from anywhere, as a read's does
// (D-510), and never `git init`.
func TestHomeHintNamesStoreAndSetup(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"--to <org>/<folder>", "--store", "--setup <setup>", "lore setup list"} {
		if !strings.Contains(notAWorkTreeHomeHint, want) {
			t.Errorf("the home hint %q does not name %s", notAWorkTreeHomeHint, want)
		}
	}
	if strings.Contains(notAWorkTreeHomeHint, "git init") {
		t.Errorf("the home hint %q says git init", notAWorkTreeHomeHint)
	}
}
