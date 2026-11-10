package host

import (
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// TestTableOfNoun: the singular command nouns whose table is not the
// noun plus "s" (underscored, -es, -ies) and the table names themselves
// resolve to the real table; anything else is not known.
func TestTableOfNoun(t *testing.T) {
	cases := map[string]string{
		"memory": "memories", "hotfix": "hotfixes", "tasklist": "task_lists", "task": "tasks",
		"techdoc": "tech_docs", "architecturenote": "architecture_notes", "cookbookrecipe": "cookbook_recipes",
		"tastepref": "taste_prefs", "workspace": "workspaces", "mission": "missions",
		"tasks": "tasks", "task_lists": "task_lists", "Memory": "memories",
		"": "", "row": "", "nosuch": "", "memor": "",
	}
	for noun, want := range cases {
		got, ok := tableOfNoun(noun)
		if got != want || ok != (want != "") {
			t.Errorf("tableOfNoun(%q) = %q, %v; want %q", noun, got, ok, want)
		}
	}
	// Every built-in table answers its own name.
	for _, def := range schema.Builtin() {
		if !def.Kind.IsZero() {
			continue
		}
		if got, ok := tableOfNoun(string(def.Name)); !ok || got != string(def.Name) {
			t.Errorf("tableOfNoun(%q) = %q, %v", def.Name, got, ok)
		}
	}
}

// TestTableOfID: every built-in prefix names the table the registry
// gives for it; ids without a known prefix are not known.
func TestTableOfID(t *testing.T) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range schema.Builtin() {
		if !def.Kind.IsZero() || def.Prefix == "" {
			continue
		}
		want, _ := reg.ByPrefix(def.Prefix)
		if got, ok := tableOfID(string(def.Prefix) + "_0123abcd"); !ok || got != string(want.Name) {
			t.Errorf("tableOfID(%s_…) = %q, %v; want %q", def.Prefix, got, ok, want.Name)
		}
	}
	for _, id := range []string{"", "mem", "zzz_00", "_00", "notanid"} {
		if got, ok := tableOfID(id); ok {
			t.Errorf("tableOfID(%q) = %q, want not known", id, got)
		}
	}
}
