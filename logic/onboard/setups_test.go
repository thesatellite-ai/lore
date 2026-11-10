package onboard

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// TestProgressStatusesPinned: schema repeats the step statuses (it sits
// below logic); the two lists must stay equal.
func TestProgressStatusesPinned(t *testing.T) {
	if !slices.Equal(StepStatuses(), schema.ProgressStatuses()) {
		t.Fatalf("onboard %v != schema %v", StepStatuses(), schema.ProgressStatuses())
	}
}

// TestIDsUnique: no step or check id appears twice.
func TestIDsUnique(t *testing.T) {
	seen := map[CheckID]bool{}
	for _, c := range CheckIDs() {
		if seen[c] {
			t.Errorf("check %s twice", c)
		}
		seen[c] = true
	}
	steps := map[StepID]bool{}
	for _, s := range StepIDs() {
		if steps[s] {
			t.Errorf("step %s twice", s)
		}
		steps[s] = true
	}
}

// TestRuleText: the rule text form round-trips.
func TestRuleText(t *testing.T) {
	r := MaxLen(64)
	if r != "max_len:64" || r.Kind() != RuleMaxLen || r.Arg() != "64" || R(RuleEmail).Arg() != "" {
		t.Errorf("rule %q kind %q arg %q", r, r.Kind(), r.Arg())
	}
}
