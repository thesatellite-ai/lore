package identity

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestReadLegacyWrongKindNamesLineAndKey: a v0.1.17 identity.yaml with a
// value of the wrong kind is E_CONFIG_INVALID naming the line and key in
// the message (top level and under a nested block), never a Go type or the
// value; unknown keys (also with a space) are ignored.
func TestReadLegacyWrongKindNamesLineAndKey(t *testing.T) {
	for body, want := range map[string]string{
		"display_name: Mira\nstable_key: [a, b]\n":            "line 2: stable_key: expected text, got a list",
		"display_name:\n  first: Mira\n":                      "line 2: display_name: expected text, got a mapping",
		"- stable_key: x\n":                                   "line 1: the file: expected a mapping (key: value lines), got a list",
		"stable_key: [\n":                                     "line 1:",
		"display_name: Mira\nmy key: 1\nnope:\n  deep: [1]\n": "",
	} {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, LegacyFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := ReadLegacy(home)
		if want == "" {
			if err != nil {
				t.Errorf("%q: %v, unknown keys are ignored", body, err)
			}
			continue
		}
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.ConfigInvalid || !strings.Contains(e.Message, LegacyFile) || !strings.Contains(e.Message, want) {
			t.Errorf("%q: %v, want E_CONFIG_INVALID with %q", body, err, want)
			continue
		}
		for _, leak := range []string{"not found in type", "cannot unmarshal", "identity.legacy", "!!", "`"} {
			if strings.Contains(e.Message, leak) {
				t.Errorf("%q: %q leaks %q", body, e.Message, leak)
			}
		}
	}
}
