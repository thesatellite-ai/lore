package yamlerr

import (
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Shapes of a hand-written file: nested blocks, a list of mappings, named
// string and struct types, a duration.
type (
	level string
	inner struct {
		Port  int      `yaml:"port"`
		Debug bool     `yaml:"debug"`
		Tags  []string `yaml:"tags"`
		Level level    `yaml:"level"`
	}
	entry struct {
		Name string `yaml:"name"`
		URL  string `yaml:"url"`
	}
	file struct {
		Name    string            `yaml:"name"`
		Inner   inner             `yaml:"inner"`
		Entries []entry           `yaml:"entries"`
		Labels  map[string]string `yaml:"labels"`
		Wait    time.Duration     `yaml:"wait"`
		Ratio   float64           `yaml:"ratio"`
	}
)

// TestDecodeProblems: every problem names its line and the key path it sits
// under, says what was expected and what kind of value the file holds, and
// never prints a Go type, yaml's internal wording or the value itself.
func TestDecodeProblems(t *testing.T) {
	cases := []struct {
		name string
		mode Mode
		body string
		want []string
	}{
		{"unknown key, top level", Strict, "name: a\nnaem: b\n", []string{"line 2: unknown key naem"}},
		{"unknown key, nested", Strict, "inner:\n  port: 1\n  prot: 2\n", []string{"line 3: unknown key inner.prot"}},
		{"unknown key, in a list item", Strict, "entries:\n  - name: kb\n    urll: x\n", []string{"line 3: unknown key entries[0].urll"}},
		{"unknown key, second list item", Strict, "entries:\n  - name: a\n  - name: b\n    zname: c\n", []string{"line 4: unknown key entries[1].zname"}},
		{"unknown key with a space", Strict, "my key: 1\n", []string{"line 1: unknown key my key"}},
		{"unknown key ignored when lenient", Lenient, "naem: b\n", nil},
		{"text for a whole number", Lenient, "inner:\n  port: soon\n", []string{"line 2: inner.port: expected a whole number, got text"}},
		{"list for text", Lenient, "name:\n  - a\n", []string{"line 2: name: expected text, got a list"}},
		{"mapping for true/false", Lenient, "inner:\n  debug:\n    x: 1\n", []string{"line 3: inner.debug: expected true or false, got a mapping"}},
		{"text for a list", Lenient, "inner:\n  tags: one\n", []string{"line 2: inner.tags: expected a list, got text"}},
		{"list for a named text type", Lenient, "inner:\n  level: [a]\n", []string{"line 2: inner.level: expected text, got a list"}},
		{"text for a named struct", Lenient, "inner: off\n", []string{"line 1: inner: expected a mapping (key: value lines), got text"}},
		{"text for a list of mappings", Lenient, "entries: none\n", []string{"line 1: entries: expected a list, got text"}},
		{"list item not a mapping", Lenient, "entries:\n  - kb\n", []string{"line 2: entries[0]: expected a mapping (key: value lines), got text"}},
		{"list for a map", Lenient, "labels: [a]\n", []string{"line 1: labels: expected a mapping (key: value lines), got a list"}},
		{"a url under the wrong key is never echoed", Lenient, "inner: https://user:pw@example.com/x\n", []string{"line 1: inner: expected a mapping (key: value lines), got text"}},
		{"text for a number", Lenient, "ratio: half\n", []string{"line 1: ratio: expected a number, got text"}},
		{"bad duration", Lenient, "wait: [1]\n", []string{"line 1: wait: expected a duration such as 30s, got a list"}},
		{"the file is a list", Lenient, "- a\n- b\n", []string{"line 1: the file: expected a mapping (key: value lines), got a list"}},
		{"two problems, both reported", Strict, "naem: a\ninner:\n  port: x\n", []string{"line 1: unknown key naem", "line 3: inner.port: expected a whole number, got text"}},
		{"syntax error keeps yaml's line", Lenient, "a: [\n", []string{"line 1: did not find expected node content"}},
		{"empty file", Strict, "", nil},
		{"comments only", Strict, "# nothing\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var f file
			err := Decode([]byte(c.body), &f, c.mode)
			if c.want == nil {
				if err != nil {
					t.Fatalf("Decode = %v, want no error", err)
				}
				return
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("Decode = %v, want an *Error", err)
			}
			if strings.Join(e.Problems, "\n") != strings.Join(c.want, "\n") {
				t.Fatalf("problems:\n%s\nwant:\n%s", strings.Join(e.Problems, "\n"), strings.Join(c.want, "\n"))
			}
			for _, p := range e.Problems {
				for _, leak := range []string{"not found in type", "cannot unmarshal", "yamlerr.", "!!", "yaml: ", "`"} {
					if strings.Contains(p, leak) {
						t.Errorf("problem %q leaks %q", p, leak)
					}
				}
			}
			if e.Error() != strings.Join(c.want, "; ") {
				t.Errorf("Error() = %q", e.Error())
			}
		})
	}
}

// TestDecodeKeepsYAMLCause: a bad value stays a *yaml.TypeError under
// errors.As (callers tell it from a syntax error); a good file decodes.
func TestDecodeKeepsYAMLCause(t *testing.T) {
	var f file
	var te *yaml.TypeError
	if err := Decode([]byte("inner:\n  port: x\n"), &f, Lenient); !errors.As(err, &te) {
		t.Fatalf("a bad value must unwrap to *yaml.TypeError: %v", err)
	}
	if err := Decode([]byte("a: [\n"), &f, Lenient); errors.As(err, &te) {
		t.Fatal("a syntax error is not a TypeError")
	}
	f = file{}
	if err := Decode([]byte("name: x\ninner:\n  port: 8\nwait: 30s\nentries:\n  - name: kb\n"), &f, Strict); err != nil ||
		f.Name != "x" || f.Inner.Port != 8 || f.Wait != 30*time.Second || len(f.Entries) != 1 {
		t.Fatalf("good file: %+v %v", f, err)
	}
}

// TestProblemsOfAnotherDecode: Problems explains an error from a decode the
// caller ran itself, and passes a non-yaml error through.
func TestProblemsOfAnotherDecode(t *testing.T) {
	raw := []byte("inner:\n  port: x\n")
	var f file
	err := yaml.Unmarshal(raw, &f)
	if got := Problems[file](raw, err); len(got) != 1 || got[0] != "line 2: inner.port: expected a whole number, got text" {
		t.Fatalf("Problems = %q", got)
	}
	if got := Problems[file](raw, errors.New("boom")); len(got) != 1 || got[0] != "boom" {
		t.Fatalf("other error = %q", got)
	}
	if Problems[file](raw, nil) != nil {
		t.Fatal("no error, no problems")
	}
}
