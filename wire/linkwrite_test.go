package wire

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestWriteThroughLink: a write to a tool file lands at the end of its
// chain of links (a link, a chain of two, a dangling link, a file in a
// linked folder), never on a middle link; a link inside the repository
// whose chain ends outside it is refused (E_REFUSED) naming the link and
// its end, whatever hop leaves; a loop is E_SYMLINK_LOOP; a link the
// person keeps outside the repository is followed.
func TestWriteThroughLink(t *testing.T) {
	outside := t.TempDir()
	victim := lwWrite(t, filepath.Join(outside, "victim.md"))
	cases := []struct {
		name  string
		setup func(t *testing.T, root string) (path, want string) // want "" = path itself
		code  errs.Code
	}{
		{"missing", func(t *testing.T, root string) (string, string) {
			return filepath.Join(root, "CLAUDE.md"), ""
		}, ""},
		{"real file", func(t *testing.T, root string) (string, string) {
			return lwWrite(t, filepath.Join(root, "CLAUDE.md")), ""
		}, ""},
		{"symlinked file", func(t *testing.T, root string) (string, string) {
			end := lwWrite(t, filepath.Join(root, "docs", "CLAUDE.md"))
			return lwLink(t, "docs/CLAUDE.md", filepath.Join(root, "CLAUDE.md")), end
		}, ""},
		{"chain of two links", func(t *testing.T, root string) (string, string) {
			end := lwWrite(t, filepath.Join(root, "shared", "real.md"))
			lwLink(t, "../shared/real.md", filepath.Join(root, "mid", "hop.md"))
			return lwLink(t, "mid/hop.md", filepath.Join(root, "CLAUDE.md")), end
		}, ""},
		{"dangling link inside", func(t *testing.T, root string) (string, string) {
			return lwLink(t, "docs/new.md", filepath.Join(root, "CLAUDE.md")), filepath.Join(root, "docs", "new.md")
		}, ""},
		{"chain ending dangling inside", func(t *testing.T, root string) (string, string) {
			lwLink(t, "gone.md", filepath.Join(root, "hop.md"))
			return lwLink(t, "hop.md", filepath.Join(root, "CLAUDE.md")), filepath.Join(root, "gone.md")
		}, ""},
		{"file in a symlinked folder", func(t *testing.T, root string) (string, string) {
			lwWrite(t, filepath.Join(root, "realrules", "a.mdc"))
			lwLink(t, "realrules", filepath.Join(root, "rules"))
			return filepath.Join(root, "rules", "a.mdc"), ""
		}, ""},
		{"link outside absolute", func(t *testing.T, root string) (string, string) {
			return lwLink(t, victim, filepath.Join(root, "CLAUDE.md")), ""
		}, errs.Refused},
		{"link outside relative", func(t *testing.T, root string) (string, string) {
			rel, err := filepath.Rel(root, victim)
			if err != nil {
				t.Fatal(err)
			}
			return lwLink(t, rel, filepath.Join(root, "CLAUDE.md")), ""
		}, errs.Refused},
		{"dangling outside", func(t *testing.T, root string) (string, string) {
			return lwLink(t, filepath.Join(outside, "nope", "x.md"), filepath.Join(root, "CLAUDE.md")), ""
		}, errs.Refused},
		{"chain leaves on the second hop", func(t *testing.T, root string) (string, string) {
			lwLink(t, victim, filepath.Join(root, "hop.md"))
			return lwLink(t, "hop.md", filepath.Join(root, "CLAUDE.md")), ""
		}, errs.Refused},
		{"chain leaves dangling on the second hop", func(t *testing.T, root string) (string, string) {
			lwLink(t, filepath.Join(outside, "nope.md"), filepath.Join(root, "hop.md"))
			return lwLink(t, "hop.md", filepath.Join(root, "CLAUDE.md")), ""
		}, errs.Refused},
		{"loop", func(t *testing.T, root string) (string, string) {
			lwLink(t, "CLAUDE.md", filepath.Join(root, "hop.md"))
			return lwLink(t, "hop.md", filepath.Join(root, "CLAUDE.md")), ""
		}, errs.SymlinkLoop},
		{"link kept outside the repo", func(t *testing.T, _ string) (string, string) {
			return lwLink(t, victim, filepath.Join(t.TempDir(), "mine.md")), victim
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := resolvedPath(t.TempDir())
			path, want := c.setup(t, root)
			if want == "" {
				want = path
			}
			got, err := WriteThroughLink(root, path)
			if c.code != "" {
				if errs.CodeOf(err) != c.code {
					t.Fatalf("want %s, got %v (%q)", c.code, err, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("want allowed, got %v", err)
			}
			if resolvedPath(filepath.Dir(got)) != resolvedPath(filepath.Dir(want)) || filepath.Base(got) != filepath.Base(want) {
				t.Fatalf("write lands on %s, want %s", got, want)
			}
		})
	}
}

// TestWriteThroughLinkNamesLinkAndEnd: the refusal names the link and the
// end of its chain (not its first hop) and says how to fix it.
func TestWriteThroughLinkNamesLinkAndEnd(t *testing.T) {
	root := resolvedPath(t.TempDir())
	end := filepath.Join(resolvedPath(t.TempDir()), "end.md")
	lwLink(t, end, filepath.Join(root, "hop.md"))
	at := lwLink(t, "hop.md", filepath.Join(root, "CLAUDE.md"))
	_, err := WriteThroughLink(root, at)
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("want *errs.Error, got %v", err)
	}
	want := "refusing to write " + at + ": it is a symbolic link to " + end + ", outside the repository"
	if e.Message != want || e.Hint == "" {
		t.Fatalf("got %q (hint %q), want %q with a hint", e.Message, e.Hint, want)
	}
}

// TestLinkEndLongChain: a chain of maxLinkHops links is followed; one more
// is a loop.
func TestLinkEndLongChain(t *testing.T) {
	dir := resolvedPath(t.TempDir())
	end := lwWrite(t, filepath.Join(dir, "end.md"))
	prev := "end.md"
	for i := range maxLinkHops {
		prev = filepath.Base(lwLink(t, prev, filepath.Join(dir, "l"+strconv.Itoa(i))))
	}
	got, isLink, err := LinkEnd(filepath.Join(dir, prev))
	if err != nil || !isLink || got != end {
		t.Fatalf("got %q %v %v, want %s", got, isLink, err, end)
	}
	one := lwLink(t, prev, filepath.Join(dir, "one-more"))
	if _, _, err := LinkEnd(one); errs.CodeOf(err) != errs.SymlinkLoop {
		t.Fatalf("want E_SYMLINK_LOOP, got %v", err)
	}
}

// lwWrite makes path (and its folder) a regular file of placeholder text.
func lwWrite(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// lwLink makes at a symlink to target (creating at's folder).
func lwLink(t *testing.T, target, at string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
	return at
}

// TestEditRepoFileThroughLinks: `lore directive` edits a tool file that is
// a link at the end of its chain (one link, a chain of two, a dangling
// link, a link to another folder of the repo), each link staying a link;
// a link leaving the repository is refused (E_REFUSED) with the outside
// file unchanged. Before, os.Root on the link's folder refused every link
// out of that folder with a bare "path escapes from parent".
func TestEditRepoFileThroughLinks(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, root, outside string) (end string)
		refuse bool
	}{
		{"real file", func(t *testing.T, root, _ string) string {
			return lwWrite(t, filepath.Join(root, "AGENTS.md"))
		}, false},
		{"link to another folder", func(t *testing.T, root, _ string) string {
			end := lwWrite(t, filepath.Join(root, "docs", "AGENTS.md"))
			lwLink(t, "docs/AGENTS.md", filepath.Join(root, "AGENTS.md"))
			return end
		}, false},
		{"chain of two links", func(t *testing.T, root, _ string) string {
			end := lwWrite(t, filepath.Join(root, "shared", "end.md"))
			lwLink(t, "../shared/end.md", filepath.Join(root, "mid", "hop.md"))
			lwLink(t, "mid/hop.md", filepath.Join(root, "AGENTS.md"))
			return end
		}, false},
		{"dangling link", func(t *testing.T, root, _ string) string {
			lwLink(t, "shared/new.md", filepath.Join(root, "AGENTS.md"))
			return filepath.Join(root, "shared", "new.md")
		}, false},
		{"chain leaving the repo", func(t *testing.T, root, outside string) string {
			end := lwWrite(t, filepath.Join(outside, "victim.md"))
			lwLink(t, end, filepath.Join(root, "mid", "hop.md"))
			lwLink(t, "mid/hop.md", filepath.Join(root, "AGENTS.md"))
			return end
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, outside := resolvedPath(t.TempDir()), resolvedPath(t.TempDir())
			end := c.setup(t, root, outside)
			a := &App{opts: Options{RepoDir: root}}
			_, err := a.EditRepoFile(t.Context(), "AGENTS.md", func(string, bool) (string, bool) { return "placeholder directive", true })
			if c.refuse {
				if errs.CodeOf(err) != errs.Refused || !strings.Contains(err.Error(), end) {
					t.Fatalf("want E_REFUSED naming %s, got %v", end, err)
				}
				if b, _ := os.ReadFile(end); string(b) != "placeholder" {
					t.Fatalf("the file outside the repo changed: %q", b)
				}
				return
			}
			if err != nil {
				t.Fatalf("want written, got %v", err)
			}
			if b, _ := os.ReadFile(end); string(b) != "placeholder directive" {
				t.Fatalf("end %s holds %q", end, b)
			}
			if c.name != "real file" {
				if fi, lerr := os.Lstat(filepath.Join(root, "AGENTS.md")); lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("AGENTS.md is no longer a link (%v)", lerr)
				}
			}
		})
	}
}

// TestLinkEndLeavesUnreadablePathsToTheWrite: a path that cannot even be
// looked at (a name too long, a file where a folder should be) is no link
// to follow; LinkEnd returns it unchanged with no error, so the write
// fails with its own exact error (userscen: E_INVALID_INPUT for a name too
// long, "not a directory" for a file in the way), not E_BAD_PATH.
func TestLinkEndLeavesUnreadablePathsToTheWrite(t *testing.T) {
	root := t.TempDir()
	file := lwWrite(t, filepath.Join(root, "afile"))
	for _, p := range []string{
		filepath.Join(root, strings.Repeat("y", 400)+".md"),
		filepath.Join(file, "x.md"),
	} {
		end, isLink, err := LinkEnd(p)
		if err != nil || isLink || end != p {
			t.Fatalf("LinkEnd(%s) = %q %v %v, want the path unchanged, no error", p, end, isLink, err)
		}
		if got, err := WriteThroughLink(root, p); err != nil || got != p {
			t.Fatalf("WriteThroughLink(%s) = %q %v", p, got, err)
		}
	}
}
