package op

// decodeerr_scen_test.go — what a REST (or --json) caller reads when its
// body does not fit: the unknown member by name, the member with the wrong
// type and the JSON value it takes, a syntax error with its byte offset,
// each E_INVALID_INPUT with the hint naming where the fields are listed.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestDecodeBodyErrorsNameTheField is an input-space matrix over the
// member kinds of addIn (string, []string, map, int, duration, id, bool).
func TestDecodeBodyErrorsNameTheField(t *testing.T) {
	t.Parallel()
	e := addOp()
	cases := []struct {
		body string
		say  []string
		hint bool
	}{
		{`{"body":"x","colour":"red"}`, []string{`unknown field "colour"`}, true},
		{`{"body":"x","Body":"y","titel":"t"}`, []string{`unknown field "titel"`}, true},
		{`{"body":5}`, []string{`field "body" takes a string, not a JSON number`}, true},
		{`{"body":"x","tags":"a"}`, []string{`field "tags" takes a list, not a JSON string`}, true},
		{`{"body":"x","tags":[1]}`, []string{`field "tags`, "takes a string, not a JSON number"}, true},
		{`{"body":"x","meta":["a"]}`, []string{`field "meta" takes an object, not a JSON array`}, true},
		{`{"body":"x","limit":"3"}`, []string{`field "limit" takes an integer, not a JSON string`}, true},
		{`{"body":"x","limit":1.5}`, []string{`field "limit" takes an integer, not a JSON number`}, true},
		{`{"body":"x","wait":"1m"}`, []string{`field "wait" takes integer nanoseconds, not a JSON string`}, true},
		{`{"body":"x","project":7}`, []string{`field "project" takes a string, not a JSON number`}, true},
		{`{"body":"x","force":"yes"}`, []string{`field "force" takes true or false, not a JSON string`}, true},
		{`{"body":"x",}`, []string{"not valid JSON", "byte"}, false},
		{`{"body":`, []string{"does not fit"}, false},
	}
	for _, c := range cases {
		_, err := e.Decode(JSONDecoder{Body: []byte(c.body)})
		if !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: want E_INVALID_INPUT, got %v", c.body, err)
			continue
		}
		env := errs.EnvelopeOf(err).Error
		for _, s := range c.say {
			if !strings.Contains(env.Message, s) {
				t.Errorf("%s: message %q lacks %q", c.body, env.Message, s)
			}
		}
		if c.hint != strings.Contains(env.Hint, "/openapi.json") {
			t.Errorf("%s: hint %q", c.body, env.Hint)
		}
	}
	// A body that fits still decodes.
	if _, err := e.Decode(JSONDecoder{Body: []byte(`{"body":"x","tags":["a"],"limit":2,"force":true}`)}); err != nil {
		t.Errorf("valid body refused: %v", err)
	}
}

// TestJSONFieldPathDropsGoTypeNames: Go 1.26 names a member of an embedded
// struct LiveFlags.live; the caller sent "live".
func TestJSONFieldPathDropsGoTypeNames(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"LiveFlags.live":     "live",
		"live":               "live",
		"meta.key":           "meta.key",
		"Outer.Inner.limit":  "limit",
		"Only":               "Only",
		"tags.LiveFlags.x_y": "tags.x_y",
	} {
		if got := jsonFieldPath(in); got != want {
			t.Errorf("jsonFieldPath(%q) = %q, want %q", in, got, want)
		}
	}
}
