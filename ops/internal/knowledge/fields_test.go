package knowledge

import (
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// TestOneOfHint: a value outside the allowed set is refused
// E_INVALID_INPUT with a hint listing every allowed value and a copyable
// flag; an allowed value and "" (not given) pass. Over every closed set
// the knowledge nouns check with OneOf.
func TestOneOfHint(t *testing.T) {
	n := Noun{Name: "rule"}
	sets := map[string][]string{
		"severity":   schema.RuleSeverities(),
		"activation": schema.RuleActivations(),
		"status":     schema.DecisionStatuses(),
		"hotfix":     schema.HotfixSeverities(),
	}
	for flag, set := range sets {
		for _, ok := range append([]string{""}, set...) {
			if err := OneOf(n, flag, ok, set); err != nil {
				t.Errorf("--%s %q: refused an allowed value: %v", flag, ok, err)
			}
		}
		err := OneOf(n, flag, "nope", set)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.InvalidInput {
			t.Fatalf("--%s nope: want E_INVALID_INPUT, got %v", flag, err)
		}
		want := "use one of: " + joinComma(set) + " (e.g. --" + flag + " " + set[0] + ")"
		if e.Hint != want {
			t.Errorf("--%s nope: hint %q, want %q", flag, e.Hint, want)
		}
	}
}

// joinComma joins with ", " (spelled out so the test does not reuse the
// code it checks).
func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}
