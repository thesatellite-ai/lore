package cli

import (
	"slices"
	"strings"
	"testing"
)

// TestNarrowNouns: a call whose words name one noun's commands without
// guessing mounts only that noun (devco:D-826: building all 677 commands
// and reading every operation's input cost each lore process ~23 ms);
// help, a flag first, a word no operation path has, or a word under a
// group gets the full tree, so cobra resolves and suggests as before.
func TestNarrowNouns(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	cases := []struct {
		args []string
		want []string // nil = the full tree
	}{
		{[]string{"memory", "list", "--json"}, []string{"memory"}},
		{[]string{"memory", "add", "some", "words"}, []string{"memory"}},
		{[]string{"memory"}, []string{"memory"}},
		{[]string{"memory", "--limit", "3"}, []string{"memory"}},
		{[]string{"secrets", "rule", "add"}, []string{"secrets"}},
		{[]string{"secrets", "rule"}, []string{"secrets"}},
		{[]string{"memory", "list", "--help"}, nil},
		{[]string{"memory", "-h"}, nil},
		{[]string{"memory", "lsit"}, nil},       // a typo: "did you mean" walks the whole tree
		{[]string{"secrets", "rule", "x"}, nil}, // a word under a group
		{[]string{"secrets", "bogus", "add"}, nil},
		{[]string{"--json", "memory", "list"}, nil},
		{[]string{"help", "memory"}, nil},
		{[]string{"completion", "bash"}, nil},
		{[]string{"nope"}, nil},
		{nil, nil},
	}
	for _, c := range cases {
		if got := NarrowNouns(f.reg, c.args); !slices.Equal(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.args, got, c.want)
		}
	}
	root := Build(f.reg, f, WithNouns([]string{"memory"}))
	if c, _, err := root.Find([]string{"memory", "list"}); err != nil || c.Name() != "list" {
		t.Fatalf("narrowed tree lost memory list: %v", err)
	}
	if c, _, _ := root.Find([]string{"trash", "purge"}); c != nil && c.Name() == "purge" {
		t.Error("narrowed to memory, the tree still mounts trash purge")
	}
	full := Build(f.reg, f)
	var names []string
	for _, c := range full.Commands() {
		names = append(names, c.Name())
	}
	if all := strings.Join(names, " "); !strings.Contains(all, "trash") || !strings.Contains(all, "memory") {
		t.Errorf("full tree: %s", all)
	}
}

// TestReadCall (devco:D-833): the read server serves a call only when its
// words resolve, as cobra resolves them, to one operation that only reads;
// a bare group runs its default verb; a write, an admin op, help, a flag
// first, a group with no default or an unknown word is left to a one-shot
// process.
func TestReadCall(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	cases := []struct {
		args []string
		want string // "" = not served
	}{
		{[]string{"memory", "list"}, "memory.list"},
		{[]string{"memory", "list", "--json", "--limit", "3"}, "memory.list"},
		{[]string{"memory", "add", "some", "words"}, ""},
		{[]string{"trash", "purge", "x"}, ""},
		{[]string{"secrets", "rule", "add"}, ""},
		{[]string{"secrets", "rule"}, ""}, // its default verb writes
		{[]string{"memory"}, "memory.list"},
		{[]string{"memory", "--limit", "3"}, "memory.list"},
		{[]string{"trash"}, ""}, // no default verb
		{[]string{"memory", "list", "--help"}, ""},
		{[]string{"memory", "list", "-h"}, ""},
		{[]string{"--json", "memory", "list"}, ""},
		{[]string{"help", "memory"}, ""},
		{[]string{"nope"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		s, ok := ReadCall(f.reg, c.args, map[string]string{"memory": "list", "secrets rule": "add"})
		if got := map[bool]string{true: s.Name, false: ""}[ok]; got != c.want {
			t.Errorf("%q: got %q, want %q", c.args, got, c.want)
		}
	}
}
