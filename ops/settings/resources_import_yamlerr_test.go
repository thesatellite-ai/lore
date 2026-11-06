package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceImportYAMLWrongKindNamesLineAndKey (YAMLERR-1): a yaml
// resource import with a value of the wrong kind (resources not a list,
// an entry's kind/name not text, fields not a mapping, in the first or a
// later entry, a token as the bad value, two problems at once) is
// E_INVALID_INPUT naming the line and key path, never a Go type, yaml's
// wording or the value, on a real import and a dry run, and writes nothing.
// Unknown keys stay ignored: an entry with an extra key imports.
func TestResourceImportYAMLWrongKindNamesLineAndKey(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`},
	)
	tok := "xoxb-" + strings.Repeat("9Z", 16) // token-shaped, built at run time
	dir := t.TempDir()
	ok := "  - kind: host\n    name: web\n    fields:\n      os: linux\n"
	for i, c := range []struct {
		name, body string
		want       []string
	}{
		{"resources is a token", "resources: " + tok + "\n", []string{"line 1: resources: expected a list, got text"}},
		{"resources is a mapping", "resources:\n  host: {name: " + tok + "}\n", []string{"line 2: resources: expected a list, got a mapping"}},
		{"kind is a list", "resources:\n  - kind: [host]\n    name: web\n", []string{"line 2: resources[0].kind: expected text, got a list"}},
		{"second name is a mapping", "resources:\n" + ok + "  - kind: host\n    name: {first: " + tok + "}\n", []string{"line 7: resources[1].name: expected text, got a mapping"}},
		{"fields is a list", "resources:\n  - kind: host\n    name: web\n    fields: [" + tok + "]\n", []string{"line 4: resources[0].fields: expected a mapping (key: value lines), got a list"}},
		{"two problems", "resources:\n  - kind: [host]\n    name: [web]\n", []string{"line 2: resources[0].kind: expected text, got a list", "line 3: resources[0].name: expected text, got a list"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(dir, "bad"+string(rune('a'+i))+".yaml")
			if err := os.WriteFile(p, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, in := range []string{`{"file":` + opstest.Quote(p) + `}`, `{"file":` + opstest.Quote(p) + `,"dry_run":true}`} {
				_, err := h.RunYes(settings.OpResImport, in)
				if !errs.Has(err, errs.InvalidInput) {
					t.Fatalf("%s: err %v, want E_INVALID_INPUT", in, err)
				}
				for _, msg := range []string{errs.EnvelopeOf(err).Error.Message, err.Error()} {
					if !strings.Contains(msg, "resource import: the YAML is not valid: line ") {
						t.Errorf("message %q", msg)
					}
					for _, w := range c.want {
						if !strings.Contains(msg, w) {
							t.Errorf("message %q lacks %q", msg, w)
						}
					}
					for _, leak := range []string{"not found in type", "cannot unmarshal", "yaml.Node", "struct {", "!!", "`", tok, "9Z9Z"} {
						if strings.Contains(msg, leak) {
							t.Errorf("message %q leaks %q", msg, leak)
						}
					}
				}
			}
		})
	}
	if s := opstest.Text(h.Must(settings.OpResList, `{"kind":"host"}`)); strings.Contains(s, "web") {
		t.Fatalf("a refused import wrote a row: %q", s)
	}
	good := filepath.Join(dir, "good.yaml")
	if err := os.WriteFile(good, []byte("version: 2\nresources:\n"+ok+"    owner: ops\n    my key: [1]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := opstest.Text(h.MustYes(settings.OpResImport, `{"file":`+opstest.Quote(good)+`}`)); !strings.Contains(s, "imported 1 rows (1 created, 0 updated) from yaml") {
		t.Errorf("unknown keys: %q", s)
	}
}
