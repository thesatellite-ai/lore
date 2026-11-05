package toolprofile

import (
	"strings"
	"testing"
)

const privDir = "~/.lore/render/"

// profileOf returns the built-in profile name.
func profileOf(t *testing.T, name string) Profile {
	t.Helper()
	reg, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reg.Lookup(name)
	if !ok {
		t.Fatalf("no built-in %s", name)
	}
	return p
}

// TestPrivateContentMatrix (CFG6, FQ13, P2-CTX-1): the private import file
// across the three import styles × what the file already holds × how many
// private renders there are. Lore's own lines (either style, so a style
// switch still drops old ones) are replaced, the person's lines stay in
// order, a Cursor file gets lore's alwaysApply frontmatter once, and a
// file left with nothing (or only lore's frontmatter) becomes "" so the
// caller deletes it.
func TestPrivateContentMatrix(t *testing.T) {
	at, read, mdc := profileOf(t, NameClaudeCode), profileOf(t, NameCodex), profileOf(t, NameCursor)
	f1, f2 := privDir+"r1/sto_a.md", privDir+"r1/sto_ü b.md"
	atLine := func(f string) string { return "@" + f }
	readLine := func(f string) string { return privateReadPrefix + f + privateReadSuffix }
	cases := []struct {
		name  string
		p     Profile
		cur   string
		files []string
		want  string
	}{
		{name: "at: empty file, no imports", p: at, cur: "", files: nil, want: ""},
		{name: "at: empty file, two imports", p: at, cur: "", files: []string{f1, f2}, want: atLine(f1) + "\n" + atLine(f2) + "\n"},
		{name: "at: user lines kept, old lore lines replaced", p: at,
			cur:   "# my notes\n@~/other.md\n" + atLine(privDir+"r1/old.md") + "\n\n",
			files: []string{f1}, want: "# my notes\n@~/other.md\n" + atLine(f1) + "\n"},
		{name: "at: lore lines only, no imports left", p: at, cur: atLine(f1) + "\n" + atLine(f2) + "\n", files: nil, want: ""},
		{name: "at: read-style old lines dropped too", p: at, cur: "mine\n" + readLine(f1) + "\n", files: []string{f2}, want: "mine\n" + atLine(f2) + "\n"},
		{name: "at: user line between lore lines keeps its place", p: at,
			cur:   atLine(f1) + "\nkeep me\n" + atLine(f2) + "\n",
			files: []string{f1}, want: "keep me\n" + atLine(f1) + "\n"},
		{name: "at: indented lore line still recognised", p: at, cur: "  " + atLine(f1) + "\n", files: nil, want: ""},
		{name: "at: no trailing newline in user file", p: at, cur: "mine", files: []string{f1}, want: "mine\n" + atLine(f1) + "\n"},
		{name: "at: import outside the render dir is the user's", p: at, cur: "@~/.lore/elsewhere.md\n", files: nil, want: "@~/.lore/elsewhere.md\n"},
		{name: "read: two imports", p: read, cur: "", files: []string{f1, f2}, want: readLine(f1) + "\n" + readLine(f2) + "\n"},
		{name: "read: at-style old line dropped", p: read, cur: atLine(f1) + "\n", files: []string{f1}, want: readLine(f1) + "\n"},
		{name: "mdc: new file gets frontmatter", p: mdc, cur: "", files: []string{f1}, want: mdcFrontmatter + readLine(f1) + "\n"},
		{name: "mdc: frontmatter only, no imports -> delete", p: mdc, cur: mdcFrontmatter + readLine(f1) + "\n", files: nil, want: ""},
		{name: "mdc: frontmatter only file", p: mdc, cur: mdcFrontmatter, files: nil, want: ""},
		{name: "mdc: frontmatter not doubled", p: mdc, cur: mdcFrontmatter + readLine(f1) + "\n", files: []string{f2}, want: mdcFrontmatter + readLine(f2) + "\n"},
		{name: "mdc: user frontmatter kept, lore's not added", p: mdc,
			cur:   "---\nalwaysApply: false\n---\nmine\n" + readLine(f1) + "\n",
			files: []string{f2}, want: "---\nalwaysApply: false\n---\nmine\n" + readLine(f2) + "\n"},
		{name: "mdc: user lines, no imports keeps file with frontmatter", p: mdc, cur: mdcFrontmatter + "mine\n", files: nil, want: mdcFrontmatter + "mine\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.p.PrivateContent(c.cur, privDir, c.files)
			if got != c.want {
				t.Fatalf("got\n%q\nwant\n%q", got, c.want)
			}
			// running twice with the same files changes nothing
			if again := c.p.PrivateContent(got, privDir, c.files); again != got {
				t.Fatalf("not idempotent:\n%q\n%q", got, again)
			}
		})
	}
}

// TestHasPrivateFile: the built-ins with a private file are exactly
// claude_code and cursor (FQ13); an import-none user profile never has
// one, even when it names a file.
func TestHasPrivateFile(t *testing.T) {
	reg, err := New([]Profile{{Name: "mine", Import: ImportNone, PointerFile: "x.md", PrivateFile: "x.local.md"}})
	if err != nil {
		t.Fatal(err)
	}
	var with []string
	for _, p := range reg.profiles {
		if p.HasPrivateFile() {
			with = append(with, p.Name)
		}
	}
	if got := strings.Join(with, ","); got != NameClaudeCode+","+NameCursor {
		t.Fatalf("profiles with a private file: %s", got)
	}
}

// TestValidatePrivateRefusals: a user profile's private_file must be a
// slash path inside the repo, outside .lore/, not its own pointer file and
// not any other profile's pointer or private file (one file, one owner).
func TestValidatePrivateRefusals(t *testing.T) {
	bad := []struct {
		priv, want string
	}{
		{"/etc/x.md", "inside the repo"},
		{"../x.md", "inside the repo"},
		{"..", "inside the repo"},
		{"a/../../x.md", "inside the repo"},
		{`a\x.md`, "inside the repo"},
		{".lore/x.md", "inside .lore/"},
		{".lore", "inside .lore/"},
		{"./.lore/x.md", "inside .lore/"},
		{"W.md", "differ from pointer_file"},
		{"./W.md", "differ from pointer_file"},
		{"CLAUDE.md", `belongs to profile "claude_code"`},
		{"CLAUDE.local.md", `belongs to profile "claude_code"`},
		{".cursor/rules/lore-private.mdc", `belongs to profile "cursor"`},
		{"AGENTS.md", `belongs to profile "codex"`},
	}
	for _, b := range bad {
		p := Profile{Name: "windsurf", PointerFile: "W.md", Import: ImportRead, PrivateFile: b.priv}
		if _, err := New([]Profile{p}); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("private_file %q: err = %v, want %q", b.priv, err, b.want)
		}
	}
	reg, err := New([]Profile{{Name: "windsurf", PointerFile: "W.md", Import: ImportRead, PrivateFile: "./local/W.local.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := reg.Lookup("windsurf"); p.PrivateFile != "local/W.local.md" || !p.HasPrivateFile() {
		t.Fatalf("private file not cleaned: %+v", p)
	}
	// a second user profile may not take the first one's private file
	if _, err := New([]Profile{
		{Name: "a", PointerFile: "A.md", Import: ImportRead, PrivateFile: "A.local.md"},
		{Name: "b", PointerFile: "B.md", Import: ImportRead, PrivateFile: "A.local.md"},
	}); err == nil || !strings.Contains(err.Error(), `belongs to profile "a"`) {
		t.Fatalf("shared private file: %v", err)
	}
	// nor use it as its pointer file
	if _, err := New([]Profile{
		{Name: "a", PointerFile: "A.md", Import: ImportRead, PrivateFile: "A.local.md"},
		{Name: "b", PointerFile: "A.local.md", Import: ImportRead},
	}); err == nil {
		t.Fatal("pointer file = another profile's private file accepted")
	}
}
