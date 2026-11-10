package rowfs

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestParseMarkerWrongKindNamesLineAndKey: a store marker with a value of
// the wrong kind is E_CONFIG_INVALID naming the line and key in the
// message, never a Go type or the value.
func TestParseMarkerWrongKindNamesLineAndKey(t *testing.T) {
	for body, want := range map[string]string{
		"lore_store: one\n":                    "line 1: lore_store: expected a whole number, got text",
		"lore_store: 1\nvisibility: [a]\n":     "line 2: visibility: expected text, got a list",
		"lore_store: 1\norg_folder:\n  x: 1\n": "line 3: org_folder: expected text, got a mapping",
		"lore_store: [\n":                      "line 1:",
	} {
		_, err := ParseMarker([]byte(body))
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.ConfigInvalid || !strings.Contains(e.Message, MarkerFile) || !strings.Contains(e.Message, want) {
			t.Errorf("%q: %v, want E_CONFIG_INVALID with %q", body, err, want)
			continue
		}
		for _, leak := range []string{"not found in type", "cannot unmarshal", "rowfs.", "!!", "`"} {
			if strings.Contains(e.Message, leak) {
				t.Errorf("%q: %q leaks %q", body, e.Message, leak)
			}
		}
	}
}
