package toolprofile

import (
	"os"
	"testing"
	"testing/fstest"
)

// fsProbe is a Probe and Finder over an in-memory repo tree (regular
// files only; a pointer file is wired when it holds lore's block).
type fsProbe struct{ fs fstest.MapFS }

func (p fsProbe) State(rel string) FileState {
	f, ok := p.fs[rel]
	switch {
	case !ok || f.Mode.IsDir():
		return FileMissing
	case HasPointer(string(f.Data)):
		return FileWired
	default:
		return FilePresent
	}
}

func (p fsProbe) Matches(pattern string) []string { return GlobFiles(p.fs, pattern) }

// TestResolveDetectsCursor: a fresh repo with a legacy .cursorrules or
// Cursor's own .cursor/rules/*.mdc rules picks the cursor profile, with
// the other tools it uses; lore's own Cursor files are no such sign; a
// wired repo keeps its targets; a Probe without Finder detects by pointer
// files only.
func TestResolveDetectsCursor(t *testing.T) {
	reg, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	codex, _ := reg.Lookup(NameCodex)
	wired := codex.StitchPointer("# repo\n", Link{FromPointer: ".lore/LORE.md", FromRoot: ".lore/LORE.md"})
	cases := []struct {
		name   string
		files  fstest.MapFS
		want   string
		reason Reason
	}{
		{"legacy .cursorrules", fstest.MapFS{".cursorrules": file("be terse\n")}, NameCursor, ReasonDetected},
		{"a cursor rule", fstest.MapFS{".cursor/rules/style.mdc": file("---\n---\nx\n")}, NameCursor, ReasonDetected},
		{".cursorrules and CLAUDE.md", fstest.MapFS{".cursorrules": file("x"), "CLAUDE.md": file("x")}, NameClaudeCode + "," + NameCursor, ReasonDetected},
		{"a folder named .cursorrules", fstest.MapFS{".cursorrules/x": file("x")}, NameCodex, ReasonFallback},
		{"a non-mdc file in rules", fstest.MapFS{".cursor/rules/notes.md": file("x")}, NameCodex, ReasonFallback},
		{"a nested mdc", fstest.MapFS{".cursor/rules/team/x.mdc": file("x")}, NameCodex, ReasonFallback},
		{"only lore's own private rule", fstest.MapFS{".cursor/rules/lore-private.mdc": file("x")}, NameCodex, ReasonFallback},
		{"only lore's path and folder rules", fstest.MapFS{".cursor/rules/lore-path-go.mdc": file("x"), ".cursor/rules/lore-dir-api.mdc": file("x")}, NameCodex, ReasonFallback},
		{"wired AGENTS.md keeps its target", fstest.MapFS{"AGENTS.md": file(wired), ".cursorrules": file("x")}, NameCodex, ReasonWired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sel := reg.Resolve(fsProbe{fs: c.files})
			if got := names(sel.Profiles); got != c.want || sel.Reason != c.reason {
				t.Fatalf("Resolve = %s (%s), want %s (%s)", got, sel.Reason, c.want, c.reason)
			}
			if len(sel.Unwired) != 0 {
				t.Fatalf("a Detect file is never named as an unwired pointer file: %v", names(sel.Unwired))
			}
		})
	}
	// Without the Finder upgrade the pointer files alone decide.
	if sel := reg.Resolve(mapProbe{}); names(sel.Profiles) != NameCodex {
		t.Fatalf("plain Probe: %s", names(sel.Profiles))
	}
	// GlobFiles over a real folder lists regular files only.
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/.cursorrules", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := GlobFiles(os.DirFS(dir), ".cursorrules"); len(got) != 1 {
		t.Fatalf("GlobFiles = %v", got)
	}
}
