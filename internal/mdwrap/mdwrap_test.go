package mdwrap

import "testing"

func TestCheck(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []int
	}{
		{"one line per paragraph", "# T\n\nA long paragraph.\n\nAnother.\n", nil},
		{"wrapped paragraph", "# T\n\nA long\nparagraph.\n", []int{4}},
		{"wrapped bullet", "- a bullet\n  continued\n- next\n", []int{2}},
		{"bullets in a row", "- a\n- b\n1. c\n2. d\n", nil},
		{"heading then text", "# T\ntext\n", nil},
		{"table", "| a | b |\n|---|---|\n| c | d |\n", nil},
		{"code block", "text:\n\n```\nline one\nline two\n```\n", nil},
		{"fence right after prose", "text:\n```\nx\ny\n```\n", nil},
		{"front matter", "---\nname: x\ndescription: |\n  a\n  b\n---\n# T\n", nil},
		{"quoted wrap", "> one\n> two\n", []int{2}},
		{"quoted paragraphs", "> one\n>\n> two\n> - item\n", nil},
		{"comment then text", "<!-- stamp -->\n# T\n", nil},
		{"nested bullet", "- a\n  - b\n", nil},
		{"long fence holds short ones", "````\nx\n```json\ny\nz\n```\nw\nv\n````\nafter\n", nil},
		{"info string never closes", "```\na\n```json\nb\n```\nc\nd\n", []int{7}},
	}
	for _, c := range cases {
		got := Check(c.text)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want lines %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i].Line != c.want[i] {
				t.Errorf("%s: got %v, want lines %v", c.name, got, c.want)
			}
		}
	}
}
