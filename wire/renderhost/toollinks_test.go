package renderhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
)

// linkCase lays out the tool file rel under root: a real file, a link, a
// chain of two links, a dangling link, a file in a linked folder, or a
// link leaving the repository. It returns the file the write must land on
// (the chain's end) and whether the write must be refused.
type linkCase struct {
	name   string
	setup  func(t *testing.T, root, rel, outside string) (end string)
	refuse bool
}

// linkCases is the shared table of the tool-file write tests.
func linkCases() []linkCase {
	return []linkCase{
		{"real file", func(t *testing.T, root, rel, _ string) string {
			return write(t, filepath.Join(root, rel))
		}, false},
		{"symlinked file", func(t *testing.T, root, rel, _ string) string {
			end := write(t, filepath.Join(root, "shared", "end"))
			link(t, end, filepath.Join(root, rel))
			return end
		}, false},
		{"chain of two links", func(t *testing.T, root, rel, _ string) string {
			end := write(t, filepath.Join(root, "shared", "end"))
			hop := link(t, end, filepath.Join(root, "mid", "hop"))
			link(t, hop, filepath.Join(root, rel))
			return end
		}, false},
		{"dangling link", func(t *testing.T, root, rel, _ string) string {
			end := filepath.Join(root, "shared", "new")
			hop := link(t, end, filepath.Join(root, "mid", "hop"))
			link(t, hop, filepath.Join(root, rel))
			return end
		}, false},
		{"file in a symlinked folder", func(t *testing.T, root, rel, _ string) string {
			real := filepath.Join(root, "realdir")
			write(t, filepath.Join(real, filepath.Base(rel)))
			link(t, real, filepath.Join(root, filepath.Dir(rel)))
			return filepath.Join(real, filepath.Base(rel))
		}, false},
		{"chain leaving the repo", func(t *testing.T, root, rel, outside string) string {
			end := write(t, filepath.Join(outside, "victim"))
			hop := link(t, end, filepath.Join(root, "mid", "hop"))
			link(t, hop, filepath.Join(root, rel))
			return end
		}, true},
	}
}

// namesEnd reports whether err names end, either as given or with its
// folders' links resolved: a refusal names the end as wire.LinkEnd found
// it, which on macOS is /private/var/... for a /var/... temp folder.
func namesEnd(err error, end string) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), end) {
		return true
	}
	// a dangling end does not exist: resolve its deepest existing folder
	for dir, tail := end, ""; ; {
		if r, rerr := filepath.EvalSymlinks(dir); rerr == nil {
			return strings.Contains(err.Error(), filepath.Join(r, tail))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		tail, dir = filepath.Join(filepath.Base(dir), tail), parent
	}
}

// checkLinkWrite asserts the outcome of one write through c's layout:
// refused with E_REFUSED and every file as it was, or the end written
// (want in it) with each link of the chain still a link.
func checkLinkWrite(t *testing.T, c linkCase, root, rel, end, want string, err error) {
	t.Helper()
	if c.refuse {
		if errs.CodeOf(err) != errs.Refused || !namesEnd(err, end) {
			t.Fatalf("want E_REFUSED naming %s, got %v", end, err)
		}
		if b, _ := os.ReadFile(end); string(b) != "x" {
			t.Fatalf("the file outside the repo changed: %q", b)
		}
		return
	}
	if err != nil {
		t.Fatalf("want written, got %v", err)
	}
	b, rerr := os.ReadFile(end)
	if rerr != nil || !strings.Contains(string(b), want) {
		t.Fatalf("end %s: %q %v, want %q in it", end, b, rerr, want)
	}
	if c.name == "real file" || c.name == "file in a symlinked folder" {
		return
	}
	for _, p := range []string{filepath.Join(root, rel), filepath.Join(root, "mid", "hop")} {
		fi, lerr := os.Lstat(p)
		if c.name == "symlinked file" && strings.HasSuffix(p, "hop") {
			continue
		}
		if lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is no longer a link (%v)", p, lerr)
		}
	}
}

// TestWriteIfChangedThroughLinks: a path rule or folder file
// (.cursor/rules/lore-path-*.mdc, <dir>/LORE.md) that is a link is written
// at its chain's end; before, the atomic rename replaced the link with a
// regular file.
func TestWriteIfChangedThroughLinks(t *testing.T) {
	for _, c := range linkCases() {
		t.Run(c.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			rel := filepath.Join(".cursor", "rules", "lore-path-src.mdc")
			end := c.setup(t, root, rel, outside)
			_, err := writeIfChanged(root, filepath.Join(root, rel), "placeholder rule")
			checkLinkWrite(t, c, root, rel, end, "placeholder rule", err)
		})
	}
}

// TestStitchPointerThroughLinks: a pointer file (CLAUDE.md) that is a link
// gets lore's pointer at its chain's end; one leaving the repository is
// refused by name (before: an internal "path escapes from parent" error,
// and a link to another folder of the repo was refused too).
func TestStitchPointerThroughLinks(t *testing.T) {
	claude := builtin(t, toolprofile.NameClaudeCode)
	for _, c := range linkCases() {
		t.Run(c.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			rel := filepath.Join("docs", "CLAUDE.md")
			end := c.setup(t, root, rel, outside)
			var w strings.Builder
			err := New(Options{}).stitchPointer(&w, root, contextTarget{profile: claude, file: filepath.ToSlash(rel)}, filepath.Join(root, ".lore", "LORE.md"))
			checkLinkWrite(t, c, root, rel, end, "LORE.md", err)
		})
	}
}

// TestWriteHookFileThroughLinks: a hook settings file
// (.claude/settings.local.json) that is a link is rewritten at its chain's
// end; before, os.WriteFile followed a link out of the repository.
func TestWriteHookFileThroughLinks(t *testing.T) {
	h, ok := toolprofile.SessionHookFor(toolprofile.NameClaudeCode)
	if !ok {
		t.Fatal("no claude_code session hook")
	}
	for _, c := range linkCases() {
		t.Run(c.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			rel := filepath.Join(".claude", "settings.local.json")
			end := c.setup(t, root, rel, outside)
			if !c.refuse && c.name != "dangling link" {
				if err := os.WriteFile(end, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := writeHookFile(root, filepath.Join(root, rel), h, false)
			checkLinkWrite(t, c, root, rel, end, h.Command(), err)
		})
	}
}

// TestCleanStaleDirsThroughLinks: removing lore's block from a folder
// pointer file that is a link writes what is left at the chain's end.
func TestCleanStaleDirsThroughLinks(t *testing.T) {
	claude := builtin(t, toolprofile.NameClaudeCode)
	for _, c := range linkCases() {
		if c.name == "dangling link" {
			continue // nothing at the end to strip a block from
		}
		t.Run(c.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			rel := filepath.Join("src", "CLAUDE.md")
			end := c.setup(t, root, rel, outside)
			body := claude.StitchPointer("placeholder kept\n", toolprofile.Link{FromPointer: "LORE.md", FromRoot: "src/LORE.md"})
			if err := os.WriteFile(end, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if c.refuse {
				// The outside file keeps lore's block; the refusal leaves it.
				if err := os.WriteFile(end, []byte("x"+body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := New(Options{}).cleanStaleDirs(root, dirPlan{All: []string{"src"}}, map[string]bool{}, func(string) {})
			if c.refuse {
				if errs.CodeOf(err) != errs.Refused || !namesEnd(err, end) {
					t.Fatalf("want E_REFUSED naming %s, got %v", end, err)
				}
				if b, _ := os.ReadFile(end); string(b) != "x"+body {
					t.Fatalf("the file outside the repo changed: %q", b)
				}
				return
			}
			checkLinkWrite(t, c, root, rel, end, "placeholder kept", err)
			if b, _ := os.ReadFile(end); strings.Contains(string(b), "LORE.md") {
				t.Fatalf("lore's block left in %s: %q", end, b)
			}
		})
	}
}

// TestFileSizeFollowsLinks: the start-size budget counts a linked file
// (~/.claude/CLAUDE.md -> ~/.agents/CLAUDE.md) as the file it ends at;
// before, a link leaving its folder counted as missing.
func TestFileSizeFollowsLinks(t *testing.T) {
	for _, c := range linkCases() {
		t.Run(c.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			rel := filepath.Join("cfg", "CLAUDE.md")
			end := c.setup(t, root, rel, outside)
			want := 1
			if c.name == "dangling link" {
				want = -1
			}
			if got := fileSize(filepath.Join(root, rel)); got != want {
				t.Fatalf("size %d, want %d (%s)", got, want, end)
			}
		})
	}
}

// TestCheckLocalFileNamesLinkEnd: a private import file that is a link
// (one hop, a chain, dangling) is refused (E_LEAK_BLOCKED, deliberate)
// naming the end of its chain; a real untracked file passes.
func TestCheckLocalFileNamesLinkEnd(t *testing.T) {
	for _, c := range linkCases() {
		if c.name == "file in a symlinked folder" {
			continue // the folder is the person's; the file itself is no link
		}
		t.Run(c.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			end := c.setup(t, root, "CLAUDE.local.md", outside)
			err := checkLocalFile(t.Context(), root, "CLAUDE.local.md")
			if c.name == "real file" {
				if err != nil {
					t.Fatalf("want allowed, got %v", err)
				}
				return
			}
			if errs.CodeOf(err) != errs.LeakBlocked || !namesEnd(err, end) {
				t.Fatalf("want E_LEAK_BLOCKED naming %s, got %v", end, err)
			}
		})
	}
}
