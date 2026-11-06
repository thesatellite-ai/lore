package attach

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestParseTypoNamesLineAndKey: a typo in .lore/stores.yaml is
// E_CONFIG_INVALID naming the line and the key path (stores[1].urll), and a
// value of the wrong kind says what was expected; never a Go type, yaml's
// wording or the value (a url may carry a token). Empty and comments-only
// files list no stores.
func TestParseTypoNamesLineAndKey(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"unknown key, top level", "stores: []\nstore:\n  - name: kb\n", "line 2: unknown key store"},
		{"unknown key, in an entry", "stores:\n  - name: kb\n    urll: file:///x\n", "line 3: unknown key stores[0].urll"},
		{"unknown key, second entry", "stores:\n  - name: kb\n    url: file:///x\n  - name: kb2\n    read-only: true\n", "line 5: unknown key stores[1].read-only"},
		{"unknown key with a space", "stores:\n  - name: kb\n    read only: true\n", "line 3: unknown key stores[0].read only"},
		{"bad type: url under stores", "stores: https://user:pw@example.com/x.git\n", "line 1: stores: expected a list, got text"},
		{"bad type: entry not a mapping", "stores:\n  - kb\n", "line 2: stores[0]: expected a mapping (key: value lines), got text"},
		{"bad type: read_only", "stores:\n  - name: kb\n    read_only: [yes]\n", "line 3: stores[0].read_only: expected true or false, got a list"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.body))
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != errs.ConfigInvalid {
				t.Fatalf("err = %v, want E_CONFIG_INVALID", err)
			}
			if !strings.Contains(e.Message, FileName) || !strings.Contains(e.Message, c.want) {
				t.Fatalf("message %q lacks %s or %q", e.Message, FileName, c.want)
			}
			for _, leak := range []string{"not found in type", "cannot unmarshal", "attach.", "!!", "`", "pw@"} {
				if strings.Contains(e.Message, leak) {
					t.Errorf("message %q leaks %q", e.Message, leak)
				}
			}
		})
	}
	for _, body := range []string{"", "# no stores yet\n"} {
		if c, err := Parse([]byte(body)); err != nil || len(c.Stores) != 0 {
			t.Fatalf("%q: %+v %v, want no stores", body, c, err)
		}
	}
}
