package wire

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCleanFileURLMatrix: file:// urls are written in one canonical
// absolute form; a leading ~ (as host or first path segment) is the user's
// home; relative paths are made absolute; non-file urls pass unchanged; a
// file url with no path is E_STORE_SCHEME.
func TestCleanFileURLMatrix(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home folder on this machine")
	}
	type urlCase struct {
		in, want string
		code     errs.Code
	}
	cases := []urlCase{
		{in: "file://~/stores/me", want: fileuri.URL(filepath.Join(home, "stores", "me"), "")},
		{in: "file:///~/stores/me", want: fileuri.URL(filepath.Join(home, "stores", "me"), "")},
		{in: "file://~", want: fileuri.URL(home, "")},
		{in: "git@github.com:acme/lore.git", want: "git@github.com:acme/lore.git"},
		{in: "https://github.com/acme/lore", want: "https://github.com/acme/lore"},
		{in: "file://", code: errs.StoreScheme},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			urlCase{in: "file:///C:/tmp/a/../b", want: "file:///C:/tmp/b"},
			urlCase{in: "file:///C:/tmp/störes", want: "file:///C:/tmp/st%C3%B6res"},
			// a drive in the host place is that drive, not a machine named
			// C: (before: file://C:/C:/Users/a/me, refused when read back)
			urlCase{in: "file://C:/Users/a/me", want: "file:///C:/Users/a/me"},
			urlCase{in: "file://d:/x/../y", want: "file:///d:/y"},
		)
	} else {
		cases = append(cases,
			urlCase{in: "file:///tmp/a/../b", want: "file:///tmp/b"},
			urlCase{in: "file:///tmp/~notme", want: "file:///tmp/~notme"},
			urlCase{in: "file:///tmp/störes", want: "file:///tmp/st%C3%B6res"},
		)
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
