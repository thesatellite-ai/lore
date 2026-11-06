package renderhost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRefuseOutsideLink: the generated out file is refused (E_REFUSED) when
// it is a link inside the repository leading outside it (absolute,
// relative, dangling, through a second link); a regular file, a missing
// file, a link kept inside the repository and an out path outside the
// repository are allowed.
func TestRefuseOutsideLink(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		setup  func(t *testing.T, root string) string // returns the out path
		refuse bool
	}{
		{"missing", func(t *testing.T, root string) string { return filepath.Join(root, ".lore", "LORE.md") }, false},
		{"regular", func(t *testing.T, root string) string {
			return write(t, filepath.Join(root, ".lore", "LORE.md"))
		}, false},
		{"link inside", func(t *testing.T, root string) string {
			write(t, filepath.Join(root, "shared", "LORE.md"))
			return link(t, "../shared/LORE.md", filepath.Join(root, ".lore", "LORE.md"))
		}, false},
		{"link outside absolute", func(t *testing.T, root string) string {
			return link(t, victim, filepath.Join(root, ".lore", "LORE.md"))
		}, true},
		{"link outside relative", func(t *testing.T, root string) string {
			rel, err := filepath.Rel(filepath.Join(root, ".lore"), victim)
			if err != nil {
				t.Fatal(err)
			}
			return link(t, rel, filepath.Join(root, ".lore", "LORE.md"))
		}, true},
		{"dangling outside", func(t *testing.T, root string) string {
			return link(t, filepath.Join(outside, "nope", "x.md"), filepath.Join(root, ".lore", "LORE.md"))
		}, true},
		{"chain leaves", func(t *testing.T, root string) string {
			hop := link(t, victim, filepath.Join(root, "hop.md"))
			return link(t, hop, filepath.Join(root, ".lore", "LORE.md"))
		}, true},
		{"out path outside the repo", func(t *testing.T, _ string) string {
			return link(t, victim, filepath.Join(t.TempDir(), "mine.md"))
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			out := c.setup(t, root)
			err := refuseOutsideLink(root, out)
			if c.refuse && errs.CodeOf(err) != errs.Refused {
				t.Fatalf("want E_REFUSED, got %v", err)
			}
			if !c.refuse && err != nil {
				t.Fatalf("want allowed, got %v", err)
			}
		})
	}
}

// write makes path (and its folder) a regular file.
func write(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// link makes at a symlink to target (creating at's folder).
func link(t *testing.T, target, at string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
	return at
}
