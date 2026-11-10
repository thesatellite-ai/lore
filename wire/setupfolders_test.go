package wire

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// browseFolder lists subfolder names only, under the home folder: the
// home root, a subfolder, no files, no hidden folders; ../ and a symlink
// that leaves home are refused, a symlinked subfolder pointing outside is
// left out, one pointing inside is listed.
func TestBrowseFolder(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{"Downloads/lore1", "code/proj", ".config/x", "Zeta"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(outside, "secret"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("not listed"), 0o600); err != nil {
		t.Fatal(err)
	}
	for link, to := range map[string]string{"escape": outside, "inside": filepath.Join(home, "code")} {
		if err := os.Symlink(to, filepath.Join(home, link)); err != nil {
			t.Fatal(err)
		}
	}
	names := func(o opssetup.BrowseOut) []string {
		var n []string
		for _, d := range o.Dirs {
			n = append(n, d.Name)
		}
		return n
	}
	cases := []struct {
		name, path string
		want       []string
		wantPath   string
		parent     string
		code       errs.Code
	}{
		{"home root", "", []string{"code", "Downloads", "inside", "Zeta"}, "~", "", ""},
		{"tilde subfolder", "~/Downloads", []string{"lore1"}, "~/Downloads", "~", ""},
		{"absolute subfolder", filepath.Join(home, "code"), []string{"proj"}, "~/code", "~", ""},
		{"through an inside link", "~/inside", []string{"proj"}, "~/code", "~", ""},
		{"dot dot out of home", "~/../outside", nil, "", "", errs.BadPath},
		{"symlink escape", "~/escape", nil, "", "", errs.BadPath},
		{"outside absolute", outside, nil, "", "", errs.BadPath},
		{"relative path", "code", nil, "", "", errs.BadPath},
		{"missing folder", "~/nope", nil, "", "", errs.BadPath},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := browseFolder(home, c.path)
			if c.code != "" {
				if !errs.Has(err, c.code) {
					t.Fatalf("err = %v, want %s", err, c.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(names(out), c.want) || out.Path != c.wantPath || out.Parent != c.parent {
				t.Errorf("got %v path %q parent %q", names(out), out.Path, out.Parent)
			}
		})
	}
}
