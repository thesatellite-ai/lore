package wire

import "testing"

// TestAttrBlockOverride is devco:Q-11: a team line after lore's block that
// sets text, eol, merge or binary for lore's files is named (CRLF text
// too); one before the block, one for other files, or a comment is not.
func TestAttrBlockOverride(t *testing.T) {
	block := "# >>> lore-sync >>>\n.lore/data/**/*.json text eol=lf merge=lore\n# <<< lore-sync <<<\n"
	for _, c := range []struct {
		name, content, want string
	}{
		{"catch-all after", block + "\n# windows team\n* text=auto eol=crlf\n*.png binary\n", "* text=auto eol=crlf"},
		{"crlf file", "# >>> lore-sync >>>\r\n.lore/data/**/*.json text eol=lf\r\n# <<< lore-sync <<<\r\n* text=auto eol=crlf\r\n", "* text=auto eol=crlf"},
		{"json binary", block + "*.json binary\n", "*.json binary"},
		{"double star folder", block + ".lore/** -text\n", ".lore/** -text"},
		{"md eol hits LORE.md", block + "*.md eol=crlf\n", "*.md eol=crlf"},
	} {
		got, ok := AttrBlockOverride(c.content, "lore-sync")
		if !ok || got != c.want {
			t.Errorf("%s: AttrBlockOverride = %q, %v; want %q", c.name, got, ok, c.want)
		}
	}
	for _, c := range []struct{ name, content string }{
		{"catch-all before", "* text=auto eol=crlf\n" + block},
		{"other files only", block + "*.png binary\nsrc/** eol=crlf\n"},
		{"diff only", block + "* linguist-vendored\n"},
		{"comment", block + "# * text=auto eol=crlf\n"},
		{"no block", "* text=auto eol=crlf\n"},
	} {
		if got, ok := AttrBlockOverride(c.content, "lore-sync"); ok {
			t.Errorf("%s: AttrBlockOverride = %q, want none", c.name, got)
		}
	}
}
