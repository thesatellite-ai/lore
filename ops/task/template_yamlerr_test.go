package task

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestTemplateFileTypoNamesLineAndKey: a typo in a task template YAML file
// (`lore task template add|edit --from`) is E_TEMPLATE naming the file, the
// line and the key path, at the top level, nested in the task tree, in a
// child and in an arg; a value of the wrong kind says what was expected;
// never a Go type, yaml's wording or the value.
func TestTemplateFileTypoNamesLineAndKey(t *testing.T) {
	const head = "name: ship\ntask:\n  title: Ship\n"
	cases := []struct {
		name string
		body string
		want string
	}{
		{"unknown key, top level", "name: ship\ndescr_x: x\ntask:\n  title: Ship\n", "line 2: unknown key descr_x"},
		{"unknown key, in the task", head + "  priorty: high\n", "line 4: unknown key task.priorty"},
		{"unknown key, in a child", head + "  children:\n    - title: a\n    - title: b\n      chekclist: [x]\n", "line 7: unknown key task.children[1].chekclist"},
		{"unknown key, in an arg", "name: ship\nargs:\n  who:\n    type: string\n    requird: true\n" + "task:\n  title: Ship\n", "line 5: unknown key args.who.requird"},
		{"unknown key with a space", "name: ship\nmy key: 1\n", "line 2: unknown key my key"},
		{"bad type: text for a list", head + "  checklist: write tests\n", "line 4: task.checklist: expected a list, got text"},
		{"bad type: text for whole numbers", head + "  after: [first]\n", "line 4: task.after[0]: expected a whole number, got text"},
		{"bad type: list for text", "name: [a, b]\n", "line 1: name: expected text, got a list"},
		{"empty file", "", "the file is empty"},
		{"comments only", "# a template\n", "the file is empty"},
		{"bad type: text for true/false", "name: ship\nargs:\n  who:\n    required: maybe\n", "line 4: args.who.required: expected true or false, got text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tpl.yaml")
			if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readTemplateFile(path)
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != errs.Template {
				t.Fatalf("err = %v, want E_TEMPLATE", err)
			}
			if !strings.Contains(e.Message, path) || !strings.Contains(e.Message, c.want) {
				t.Fatalf("message %q lacks the file or %q", e.Message, c.want)
			}
			for _, leak := range []string{"not found in type", "cannot unmarshal", "task.Template", "!!", "`"} {
				if strings.Contains(e.Message, leak) {
					t.Errorf("message %q leaks %q", e.Message, leak)
				}
			}
		})
	}
}
