package yamlfmt

import (
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestFlowKeys: FlowKeys names exactly what Block would rewrite (a flow
// list or mapping holding a list or mapping, outermost only, in file
// order), never a flat scalar flow list, and only reads the node.
func TestFlowKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, raw string
		want      []string
	}{
		{"block file", "identity:\n  key: k\nsetups:\n  - name: a\n", nil},
		{"empty file", "", nil},
		{"empty flow list", "setups: []\n", nil},
		{"flat scalar flow lists stay", "tools: [claude, codex]\nidentity: {key: k, name: n}\nsetups:\n  - name: a\n    folders: [~/a/**]\n", nil},
		{"setups on one line", "identity:\n  key: k\nsetups: [{name: personal, kind: solo, identity: {key: k}}]\n", []string{"setups"}},
		{"nested under a block list item", "setups:\n  - name: a\n    github: {hosts: [{alias: x}]}\n", []string{"setups.0.github"}},
		{"two keys in file order", "repo_orgs: {prj_1: {org: a}}\nsetups: [{name: b}]\n", []string{"repo_orgs", "setups"}},
		{"outermost only", "setups: [{name: a, identity: {key: k}}, {name: b}]\n", []string{"setups"}},
		{"top level itself", "{setups: [{name: a}]}\n", []string{"."}},
		{"unicode key", "réglages: [{nom: é}]\n", []string{"réglages"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(c.raw), &doc); err != nil {
				t.Fatal(err)
			}
			got := FlowKeys(&doc)
			if !slices.Equal(got, c.want) {
				t.Fatalf("FlowKeys = %q, want %q", got, c.want)
			}
			// Reading only: the node still encodes as the file did when it
			// had flow keys (Block was not run on it).
			if len(c.want) > 0 && len(FlowKeys(&doc)) != len(c.want) {
				t.Fatal("FlowKeys changed the node")
			}
			// After Block nothing is left to rewrite.
			Block(&doc)
			if left := FlowKeys(&doc); len(left) != 0 {
				t.Fatalf("after Block FlowKeys = %q", left)
			}
		})
	}
}
