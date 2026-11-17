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

// TestStitchPointerLinkOutSkipped (D-502): CLAUDE.md linked in from
// outside the repository (another repository's CLAUDE.md, a notes repo)
// is never written through, whatever its end holds (lore's block already,
// a stale block, none): the end stays byte-identical, the link stays a
// link, and the render goes on with one line naming the link, its end and
// where lore's context is instead.
func TestStitchPointerLinkOutSkipped(t *testing.T) {
	stale := builtin(t, toolprofile.NameClaudeCode).StitchPointer("# notes kept by hand\n", toolprofile.Link{FromPointer: "old/LORE.md", FromRoot: "old/LORE.md"})
	for name, body := range map[string]string{
		"identical block": claudeBlockFor(t, "# notes kept by hand\n"),
		"stale block":     stale,
		"no block":        "# notes kept by hand\n",
	} {
		t.Run(name, func(t *testing.T) {
			root, at, end := pointerLinkFixture(t, body)
			out, err := stitchClaude(t, root)
			if err != nil {
				t.Fatalf("want a note, not a failure: %v (%s)", err, out)
			}
			if b, _ := os.ReadFile(end); string(b) != body {
				t.Fatalf("the end outside the repository was written: %q", b)
			}
			endReal, _ := filepath.EvalSymlinks(end)
			for _, want := range []string{at + wire.PointerSkippedMark, endReal, "outside the repository", "lore context"} {
				if !strings.Contains(out, want) {
					t.Fatalf("note lacks %q: %q", want, out)
				}
			}
			assertStillLink(t, at)
		})
	}
}

// TestStitchPointerLinkInsideWritten: CLAUDE.md linked to a regular file
// inside the repository is written through at its end; the link stays.
func TestStitchPointerLinkInsideWritten(t *testing.T) {
	root := t.TempDir()
	end := filepath.Join(root, "docs", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(end), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(end, []byte("# own text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := link(t, filepath.Join("docs", "CLAUDE.md"), filepath.Join(root, "CLAUDE.md"))
	if out, err := stitchClaude(t, root); err != nil {
		t.Fatalf("want ok, got %v (%s)", err, out)
	}
	if b, _ := os.ReadFile(end); !toolprofile.HasPointer(string(b)) || !strings.Contains(string(b), "# own text") {
		t.Fatalf("end: %q", b)
	}
	assertStillLink(t, at)
}

// TestStitchPointerLinkSkips: a link render does not write through is left
// as it is with a note naming the file and why, never an error: dangling
// out of the repo, a folder, another kind of file outside the repo (each
// leaves the repository: D-502).
func TestStitchPointerLinkSkips(t *testing.T) {
	cases := []struct {
		name, why string
		setup     func(t *testing.T, root, outside string) (end string)
	}{
		{"dangling", "outside the repository", func(t *testing.T, root, outside string) string {
			end := filepath.Join(outside, "gone", "CLAUDE.md")
			link(t, end, filepath.Join(root, "CLAUDE.md"))
			return end
		}},
		{"folder", "outside the repository", func(t *testing.T, root, outside string) string {
			end := filepath.Join(outside, "CLAUDE.md")
			if err := os.MkdirAll(end, 0o700); err != nil {
				t.Fatal(err)
			}
			link(t, end, filepath.Join(root, "CLAUDE.md"))
			return end
		}},
		{"other kind of file", "outside the repository", func(t *testing.T, root, outside string) string {
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
