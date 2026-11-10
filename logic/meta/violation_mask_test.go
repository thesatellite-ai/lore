package meta

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

// TestViolationMasksSensitive: with Rules.Sensitive a pattern, min or max
// message never quotes the value (3.20); without it the value is named;
// Sensitive alone checks nothing.
func TestViolationMasksSensitive(t *testing.T) {
	cases := []struct {
		typ  Type
		raw  string
		r    Rules
		want string
	}{
		{Type{Kind: value.KindText}, `"probe-1"`, Rules{Pattern: "^zz"}, `"probe-1" does not match`},
		{Type{Kind: value.KindNumber}, `500`, Rules{Max: "100"}, "500 is above"},
		{Type{Kind: value.KindNumber}, `-5`, Rules{Min: "0"}, "-5 is below"},
		{Type{Kind: value.KindList, Elem: value.KindText}, `["ok","probe-2"]`, Rules{Pattern: "^ok$"}, `"probe-2"`},
	}
	for _, c := range cases {
		plain, err := Violation("k", c.typ, []byte(c.raw), c.r)
		if err != nil || !strings.Contains(plain, c.want) {
			t.Errorf("%s plain: %q %v", c.raw, plain, err)
		}
		c.r.Sensitive = true
		masked, err := Violation("k", c.typ, []byte(c.raw), c.r)
		if err != nil || masked == "" || !strings.Contains(masked, "•••") {
			t.Errorf("%s masked: %q %v", c.raw, masked, err)
		}
		for _, leak := range []string{"probe", "500", "-5"} {
			if strings.Contains(c.raw, leak) && strings.Contains(masked, leak) {
				t.Errorf("%s masked message leaks %q: %q", c.raw, leak, masked)
			}
		}
	}
	if !(Rules{Sensitive: true}).Empty() {
		t.Error("Sensitive alone is not empty")
	}
}
