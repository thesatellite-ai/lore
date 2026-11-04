package settings

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// forms gives a field's two forms for raw JSON: as the engine builds it in
// memory (memory) and as row.Decode infers it from the file (value.Infer).
func inferred(t *testing.T, raw string) value.Value {
	t.Helper()
	v, err := value.Infer([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func jsonOf(t *testing.T, raw string) value.Value {
	t.Helper()
	v, err := value.JSONOf([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func listOf(t *testing.T, items ...string) value.Value {
	t.Helper()
	v, err := textList(items)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestTextsBothForms: texts reads a list field the same from a List, a
// JSON array and the inferred file form; a scalar is one item; absent and
// empty give none.
func TestTextsBothForms(t *testing.T) {
	cases := []struct {
		name string
		v    value.Value
		want []string
	}{
		{"memory list", listOf(t, "prod", "staging"), []string{"prod", "staging"}},
		{"json array", jsonOf(t, `["prod","staging"]`), []string{"prod", "staging"}},
		{"file form", inferred(t, `["prod","staging"]`), []string{"prod", "staging"}},
		{"unicode", inferred(t, `["ünï","日本"]`), []string{"ünï", "日本"}},
		{"scalar", inferred(t, `"solo"`), []string{"solo"}},
		{"empty list", inferred(t, `[]`), nil},
		{"absent", nil, nil},
		{"null", value.Null{}, nil},
	}
	for _, c := range cases {
		r := row.Row{Fields: map[string]value.Value{}}
		if c.v != nil {
			r.Fields["values"] = c.v
		}
		got := texts(r, "values")
		if len(got) != len(c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %v, want %v", c.name, got, c.want)
			}
		}
	}
}

// TestFromJSONAndDisplayBothForms: a json field (a variable's value, a
// policy value) prints the same whichever form it was read in.
func TestFromJSONAndDisplayBothForms(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"text", `"https://a.example"`, "https://a.example"},
		{"number", `42`, "42"},
		{"bool", `false`, "false"},
		{"list", `["task.delete","rule.add"]`, "task.delete, rule.add"},
		{"object", `{"a":1}`, `{"a":1}`},
		{"unicode", `"ünï 日本"`, "ünï 日本"},
	}
	for _, c := range cases {
		mem := display(fromJSON(jsonOf(t, c.raw)))
		file := display(fromJSON(inferred(t, c.raw)))
		if mem != c.want || file != c.want {
			t.Errorf("%s: memory %q file %q, want %q", c.name, mem, file, c.want)
		}
	}
	if got := display(listOf(t, "a", "b")); got != "a, b" {
		t.Errorf("memory List %q", got)
	}
	if got := display(nil); got != "" {
		t.Errorf("nil %q", got)
	}
}
