package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCleanFileURLMatrix: file:// urls are written in one canonical
// absolute form; a leading ~ (as host or first path segment) is the user's
// home; relative paths are made absolute; non-file urls pass unchanged; a
// file url with no path is E_STORE_SCHEME.
func TestCleanFileURLMatrix(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home folder on this machine")
	}
	h := filepath.ToSlash(home)
	cases := []struct {
		in, want string
		code     errs.Code
	}{
		{in: "file://~/stores/me", want: "file://" + h + "/stores/me"},
		{in: "file:///~/stores/me", want: "file://" + h + "/stores/me"},
		{in: "file://~", want: "file://" + h},
		{in: "file:///tmp/a/../b", want: "file:///tmp/b"},
		{in: "file:///tmp/~notme", want: "file:///tmp/~notme"},
		{in: "file:///tmp/störes", want: "file:///tmp/st%C3%B6res"},
		{in: "git@github.com:acme/lore.git", want: "git@github.com:acme/lore.git"},
		{in: "https://github.com/acme/lore", want: "https://github.com/acme/lore"},
		{in: "file://", code: errs.StoreScheme},
	}
	for _, c := range cases {
		got, err := cleanFileURL(c.in)
		if c.code != "" {
			if !errs.Has(err, c.code) {
				t.Errorf("%s: want %s, got %q %v", c.in, c.code, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %q %v, want %q", c.in, got, err, c.want)
		}
	}
}
