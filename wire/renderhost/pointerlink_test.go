package renderhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/wire"
)

// pointerLinkFixture is a repository with CLAUDE.md linked in from a notes
// folder outside it (a symlink manager's layout); it returns the repo
// root, the link and the link's end.
func pointerLinkFixture(t *testing.T, endBody string) (root, at, end string) {
	t.Helper()
	root, notes := t.TempDir(), t.TempDir()
	end = filepath.Join(notes, "project", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(end), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(end, []byte(endBody), 0o600); err != nil {
		t.Fatal(err)
	}
	at = link(t, end, filepath.Join(root, "CLAUDE.md"))
	return root, at, end
}

// stitchClaude runs stitchPointer for CLAUDE.md at the repo root.
func stitchClaude(t *testing.T, root string) (string, error) {
	t.Helper()
	var w strings.Builder
	err := New(Options{}).stitchPointer(&w, root, contextTarget{profile: builtin(t, toolprofile.NameClaudeCode), file: "CLAUDE.md"}, filepath.Join(root, ".lore", "LORE.md"))
	return w.String(), err
}

// claudeBlockFor is the CLAUDE.md text lore writes for LORE.md at the
// default place, after the person's own text.
func claudeBlockFor(t *testing.T, own string) string {
	t.Helper()
	return builtin(t, toolprofile.NameClaudeCode).StitchPointer(own, toolprofile.Link{FromPointer: ".lore/LORE.md", FromRoot: ".lore/LORE.md"})
}

// assertStillLink fails when at is no longer a symbolic link.
func assertStillLink(t *testing.T, at string) {
	t.Helper()
	if fi, err := os.Lstat(at); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer a link (%v)", at, err)
	}
}

// TestStitchPointerLinkOutIdentical: CLAUDE.md linked from outside the
// repository whose end already holds the exact block is left untouched
// (not even rewritten) and the render goes on.
func TestStitchPointerLinkOutIdentical(t *testing.T) {
	body := claudeBlockFor(t, "# notes kept by hand\n")
	root, at, end := pointerLinkFixture(t, body)
	before, err := os.Stat(end)
	if err != nil {
		t.Fatal(err)
	}
	out, err := stitchClaude(t, root)
	if err != nil {
		t.Fatalf("want ok, got %v (%s)", err, out)
	}
	if !strings.Contains(out, "pointer present") {
		t.Fatalf("want pointer present, got %q", out)
	}
	after, _ := os.Stat(end)
	if b, _ := os.ReadFile(end); string(b) != body || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the end was rewritten: %q", b)
	}
	assertStillLink(t, at)
}

// TestStitchPointerLinkOutStale: an end whose block is stale is updated in
// place through the link: the person's text stays, the link stays a link,
// the end keeps its mode and no temp file is left beside it.
func TestStitchPointerLinkOutStale(t *testing.T) {
	stale := builtin(t, toolprofile.NameClaudeCode).StitchPointer("# notes kept by hand\n", toolprofile.Link{FromPointer: "old/LORE.md", FromRoot: "old/LORE.md"})
	root, at, end := pointerLinkFixture(t, stale)
	out, err := stitchClaude(t, root)
	if err != nil {
		t.Fatalf("want ok, got %v (%s)", err, out)
	}
	b, _ := os.ReadFile(end)
	if string(b) != claudeBlockFor(t, "# notes kept by hand\n") || strings.Contains(string(b), "old/LORE.md") {
		t.Fatalf("end not updated in place: %q", b)
	}
	if fi, _ := os.Stat(end); fi.Mode().Perm() != 0o600 {
		t.Fatalf("end mode %v, want 0600 kept", fi.Mode().Perm())
	}
	assertStillLink(t, at)
	entries, _ := os.ReadDir(filepath.Dir(end))
	if len(entries) != 1 {
		t.Fatalf("want only CLAUDE.md beside the end, got %v", entries)
	}
}

// TestStitchPointerLinkOutNoBlock: an end of the same file name without a
// block yet gets one (written through), its text kept.
func TestStitchPointerLinkOutNoBlock(t *testing.T) {
	root, at, end := pointerLinkFixture(t, "# notes kept by hand\n")
	if out, err := stitchClaude(t, root); err != nil {
		t.Fatalf("want ok, got %v (%s)", err, out)
	}
	if b, _ := os.ReadFile(end); !toolprofile.HasPointer(string(b)) || !strings.Contains(string(b), "# notes kept by hand") {
		t.Fatalf("end: %q", b)
	}
	assertStillLink(t, at)
}

// TestStitchPointerLinkSkips: a link render does not write through is left
// as it is with a note naming the file and why, never an error: dangling
// out of the repo, a folder, another kind of file outside the repo.
func TestStitchPointerLinkSkips(t *testing.T) {
	cases := []struct {
		name, why string
		setup     func(t *testing.T, root, outside string) (end string)
	}{
		{"dangling", "does not exist", func(t *testing.T, root, outside string) string {
			end := filepath.Join(outside, "gone", "CLAUDE.md")
			link(t, end, filepath.Join(root, "CLAUDE.md"))
			return end
		}},
		{"folder", "is a folder", func(t *testing.T, root, outside string) string {
			end := filepath.Join(outside, "CLAUDE.md")
			if err := os.MkdirAll(end, 0o700); err != nil {
				t.Fatal(err)
			}
			link(t, end, filepath.Join(root, "CLAUDE.md"))
			return end
		}},
		{"other kind of file", "neither a CLAUDE.md", func(t *testing.T, root, outside string) string {
			end := write(t, filepath.Join(outside, "profile.sh"))
			link(t, end, filepath.Join(root, "CLAUDE.md"))
			return end
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			end := c.setup(t, root, outside)
			out, err := stitchClaude(t, root)
			if err != nil {
				t.Fatalf("want a note, not a failure: %v", err)
			}
			at := filepath.Join(root, "CLAUDE.md")
			if !strings.Contains(out, at+wire.PointerSkippedMark) || !strings.Contains(out, c.why) {
				t.Fatalf("want a note naming %s and %q, got %q", at, c.why, out)
			}
			assertStillLink(t, at)
			if fi, err := os.Stat(end); err == nil && fi.Mode().IsRegular() {
				if b, _ := os.ReadFile(end); string(b) != "x" {
					t.Fatalf("the end changed: %q", b)
				}
			}
		})
	}
}

// TestStitchPointerRegularFileUnchanged: a plain CLAUDE.md is written in
// place as before, and an identical one is left as it is.
func TestStitchPointerRegularFileUnchanged(t *testing.T) {
	root := t.TempDir()
	at := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(at, []byte("# own text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := stitchClaude(t, root)
	if err != nil || strings.Contains(out, wire.PointerSkippedMark) {
		t.Fatalf("got %v %q", err, out)
	}
	want := claudeBlockFor(t, "# own text\n")
	if b, _ := os.ReadFile(at); string(b) != want {
		t.Fatalf("CLAUDE.md: %q", b)
	}
	if out, err = stitchClaude(t, root); err != nil || !strings.Contains(out, "pointer present") {
		t.Fatalf("second run: %v %q", err, out)
	}
	if fi, _ := os.Lstat(at); fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		t.Fatal("CLAUDE.md is no longer a regular file")
	}
}
