package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestMetaJSONErrorsNameMetaJSON (devco:L-5): a --meta-json argument with
// no key or with bad JSON is named in --meta-json's own terms (key=<json>,
// the parser's reason and byte offset), never with the --meta grammar
// ("has no operator / write key=value"); a good one parses.
func TestMetaJSONErrorsNameMetaJSON(t *testing.T) {
	t.Parallel()
	for arg, want := range map[string]string{
		"{bad":     `--meta-json "{bad" has no key`,
		"k={bad":   "--meta-json k: the value is not valid JSON (invalid character 'b' looking for beginning of object key string at byte 2)",
		`k={"a":1`: "--meta-json k: the value is not valid JSON (it ends before the JSON is complete)",
	} {
		_, err := MetaFlags{MetaJSON: []string{arg}}.Write("", ids.AnyID{}, nil)
		var e *errs.Error
		if !errors.As(err, &e) || !strings.Contains(e.Message, want) || strings.Contains(e.Message, "has no operator") || !strings.Contains(e.Hint, `key={"name":"value"}`) {
			t.Errorf("%s: %v", arg, err)
		}
	}
	if _, err := (MetaFlags{MetaJSON: []string{`k={"a":1}`}}).Write("", ids.AnyID{}, nil); err != nil {
		t.Errorf("good JSON: %v", err)
	}
}
