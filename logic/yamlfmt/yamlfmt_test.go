package yamlfmt

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestIndent: the step of the first nested mapping key, whatever lists,
// comments and blank lines come first; 2 when the file shows none.
func TestIndent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, raw string
		want      int
	}{
		{"empty", "", 2},
		{"flat", "a: 1\nb: 2\n", 2},
		{"two", "personal:\n  store: x\n", 2},
		{"four", "# head\npersonal:\n    store: x # c\n", 4},
		{"list item at the parent column, then 2", "setups:\n- name: a\n  kind: solo\nlive:\n  autopush: true\n", 2},
		{"list item under 2, its keys at 4 do not count", "setups:\n  - name: a\n    kind: solo\nlive:\n  autopush: true\n", 2},
		// The list's own indent wins over a key nested in an item: yaml.v3
		// writes that key 2 deeper at every indent.
		{"nested in a list item, the list item decides", "setups:\n  - name: a\n    github:\n        user: x\n", 2},
		{"yaml.v3 at 4: items at 4, keys in an item 2 deeper", "orgs:\n    - name: acme\nsetups:\n    - name: personal\n      identity:\n        key: x\n", 4},
		{"a top-level key beats a nested one", "setups:\n    - name: a\n      identity:\n        key: x\nlive:\n    autopush: true\n", 4},
		{"nested only inside items", "- name: a\n  github:\n      user: x\n", 4},
		{"flow mapping", "identity: {key: x}\n", 2},
		{"one space is not a YAML indent lore writes", "a:\n b: 1\n", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Indent([]byte(c.raw)); got != c.want {
				t.Fatalf("Indent = %d, want %d", got, c.want)
			}
		})
	}
}

// encodeAfter parses raw, lets edit change the top mapping, and encodes it
// with raw's own indent.
func encodeAfter(t *testing.T, raw string, edit func(m *yaml.Node)) string {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc.Content[0])
	out, err := Encode(&doc, Indent([]byte(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// removeKey drops key i (0-based) of m, keeping the header.
func removeKey(i int) func(m *yaml.Node) {
	return func(m *yaml.Node) {
		KeepHeader(m, 2*i)
		m.Content = append(m.Content[:2*i], m.Content[2*i+2:]...)
	}
}

// appendEntry appends `{name: personal, kind: solo}` (block style, as a
// writer builds it) to the setups list of m.
func appendEntry(m *yaml.Node) {
	ent := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "name"}, {Kind: yaml.ScalarNode, Value: "personal"},
		{Kind: yaml.ScalarNode, Value: "kind"}, {Kind: yaml.ScalarNode, Value: "solo"},
	}}
	m.Content[1].Content = append(m.Content[1].Content, ent)
}

// TestDropEmpty: an emptied list or mapping goes with its key; a full one,
// a scalar or an absent key stays.
func TestDropEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, raw, key, want string
		dropped              bool
	}{
		{"empty list", "a: 1\nsetups: []\n", "setups", "a: 1\n", true},
		{"empty mapping", "a: 1\nrepo_setups: {}\n", "repo_setups", "a: 1\n", true},
		{"full list stays", "setups: [x]\n", "setups", "setups: [x]\n", false},
		{"scalar stays", "setups: x\n", "setups", "setups: x\n", false},
		{"absent key", "a: 1\n", "setups", "a: 1\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var dropped bool
			got := encodeAfter(t, c.raw, func(m *yaml.Node) { dropped = DropEmpty(m, c.key) })
			if got != c.want || dropped != c.dropped {
				t.Fatalf("got %q (dropped %v), want %q (dropped %v)", got, dropped, c.want, c.dropped)
			}
		})
	}
}

// TestEncode: round trips keep the indent; emptying the file leaves its
// comments or nothing, never `{}`; the header survives its key.
func TestEncode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, raw string
		edit      func(m *yaml.Node)
		want      string
	}{
		{"2 spaces kept", "personal:\n  store: x\nlive:\n  autopush: true\n", func(*yaml.Node) {}, "personal:\n  store: x\nlive:\n  autopush: true\n"},
		{"4 spaces kept", "personal:\n    store: x\n", func(*yaml.Node) {}, "personal:\n    store: x\n"},
		{"last key gone: empty file", "personal:\n  store: x\n", removeKey(0), ""},
		{"last key gone: its header stays", "# my machine\npersonal:\n  store: x\n", removeKey(0), "# my machine\n"},
		{"header blank-line separated stays", "# my machine\n\npersonal:\n  store: x\n", removeKey(0), "# my machine\n"},
		{"first key gone: header moves on", "# my machine\nidentity:\n  key: k\nlive:\n  autopush: true\n", removeKey(0), "# my machine\nlive:\n  autopush: true\n"},
		{"middle key gone", "a: 1\nb: 2\nc: 3\n", removeKey(1), "a: 1\nc: 3\n"},
		{"flat scalar flow lists stay as written", "tools: [claude, codex]\nidentity: {key: k, name: n}\nsetups:\n  - name: a\n    folders: [~/a/**]\n", func(*yaml.Node) {}, "tools: [claude, codex]\nidentity: {key: k, name: n}\nsetups:\n  - name: a\n    folders: [~/a/**]\n"},
		{"a file already flow is repaired", "identity:\n  key: k\nsetups: [{name: personal, kind: solo, identity: {key: k, name: n}, tools: [claude]}]\n", func(*yaml.Node) {},
			"identity:\n  key: k\nsetups:\n  - name: personal\n    kind: solo\n    identity:\n      key: k\n      name: n\n    tools:\n      - claude\n"},
		{"the flow line's comment stays on its key", "repo_orgs:\n  prj_1: {org: a, remotes: [x]} # pinned\n  prj_2: b\n", func(*yaml.Node) {},
			"repo_orgs:\n  prj_1: # pinned\n    org: a\n    remotes:\n      - x\n  prj_2: b\n"},
		{"an entry appended into `key: []` is block", "setups: []\n", appendEntry, "setups:\n  - name: personal\n    kind: solo\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := encodeAfter(t, c.raw, c.edit); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestDecodeKeepsHandWrittenLayout: a hand-written CRLF file whose last
// lines are a column-0 comment is written back with CRLF endings, the
// header comment lines still together and the trailing comment still last
// at column 0 after a blank line (yaml.v3 alone hangs it on the last
// setup's last key).
func TestDecodeKeepsHandWrittenLayout(t *testing.T) {
	t.Parallel()
	raw := []byte("# head one\r\n# head two\r\nsetups:\r\n  - name: a\r\n    folders:\r\n        - ~/a/**\r\n# trailing\r\n# two")
	var doc yaml.Node
	if err := Decode(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out, err := EncodeLike(&doc, raw)
	if err != nil {
		t.Fatal(err)
	}
	want := "# head one\r\n# head two\r\nsetups:\r\n  - name: a\r\n    folders:\r\n      - ~/a/**\r\n\r\n# trailing\r\n# two\r\n"
	if string(out) != want {
		t.Errorf("got  %q\nwant %q", out, want)
	}
	// An indented trailing comment belongs to its key and stays there.
	raw = []byte("a:\n  b: 1\n  # under a\n")
	doc = yaml.Node{}
	if err := Decode(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.FootComment != "" {
		t.Errorf("an indented comment was moved to the file's end: %q", doc.FootComment)
	}
}
