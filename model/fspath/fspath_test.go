package fspath

import (
	"os"
	"path/filepath"
	"testing"
)

// linkWorld is base/real with a folder inside, and base/link -> base/real:
// the macOS /var -> /private/var shape (devco D-413) on any system.
func linkWorld(t *testing.T) (real, link string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real, link = filepath.Join(base, "real"), filepath.Join(base, "link")
	if err := os.MkdirAll(filepath.Join(real, "repo", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	return real, link
}

// TestCanonicalThroughLink (devco D-413): a folder reached through a link,
// an existing one or one not made yet, has one canonical form, the real
// one; Same, Within and Rel agree whichever spelling each side uses.
func TestCanonicalThroughLink(t *testing.T) {
	real, link := linkWorld(t)
	for _, c := range []struct{ in, want string }{
		{filepath.Join(link, "repo"), filepath.Join(real, "repo")},
		{filepath.Join(link, "repo", "sub") + string(filepath.Separator), filepath.Join(real, "repo", "sub")},
		// missing tail: the lock / clone that is about to be made
		{filepath.Join(link, "repo", ".lore", "state", "migrate.lock"), filepath.Join(real, "repo", ".lore", "state", "migrate.lock")},
		{filepath.Join(link, "gone", "..", "repo"), filepath.Join(real, "repo")},
		{"", ""},
	} {
		if got := Canonical(c.in); got != c.want {
			t.Errorf("Canonical(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	lr, rr := filepath.Join(link, "repo"), filepath.Join(real, "repo")
	if !Same(lr, rr) || !Same(rr+"/", lr) || Same(lr, real) || Same("", lr) || !Same("", "") {
		t.Error("Same disagrees with the canonical forms")
	}
	if rel, ok := Rel(lr, filepath.Join(real, "repo", "sub")); !ok || rel != "sub" {
		t.Errorf("Rel(linked root, real sub) = %q %v", rel, ok)
	}
	if rel, ok := Rel(rr, lr); !ok || rel != "." {
		t.Errorf("Rel(root, itself through the link) = %q %v", rel, ok)
	}
	if !Within(filepath.Join(link, "repo", "sub", "new.json"), rr) || Within(real, lr) || Within(filepath.Join(real, "repo2"), lr) {
		t.Error("Within disagrees")
	}
}

// TestInTypedKeepsThePersonsSpelling: a folder lore derived (a git top, in
// its real form) is echoed in the spelling the person typed, never as the
// real path, and unchanged when the typed path is not inside it.
func TestInTypedKeepsThePersonsSpelling(t *testing.T) {
	real, link := linkWorld(t)
	typed := filepath.Join(link, "repo", "sub")
	if got := InTyped(typed, filepath.Join(real, "repo")); got != filepath.Join(link, "repo") {
		t.Errorf("InTyped = %q, want the linked spelling", got)
	}
	if got := InTyped(filepath.Join(real, "repo"), filepath.Join(real, "repo", "sub")); got != filepath.Join(real, "repo", "sub") {
		t.Errorf("InTyped outside = %q, want the canonical path back", got)
	}
}
