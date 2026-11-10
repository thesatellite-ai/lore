package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// estimateUnitKey is the policy key OWNERQ-21 narrows.
const estimateUnitKey = "tasks.estimate_unit"

// TestEstimateUnitDaysRefusedAtEveryLevel (OWNERQ-21, G-META-8): `policy
// set tasks.estimate_unit days` is refused E_INVALID_INPUT at the repo, a
// store and a store folder, confirmed or not, the hint names hours or
// points, and nothing is written (the key keeps its default); points and
// hours still set at every level.
func TestEstimateUnitDaysRefusedAtEveryLevel(t *testing.T) {
	t.Parallel()
	h := multiStore(t)
	for _, to := range []string{"", "kb", "kb/web"} {
		_, err := h.RunYes(settings.OpPolicySet, setIn(estimateUnitKey, "days", to))
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "tasks.estimate_unit hours") {
			t.Fatalf("days --to %q: %v (hint %q), want E_INVALID_INPUT naming hours", to, err, errs.EnvelopeOf(err).Error.Hint)
		}
		if r := showRules(t, h, estimateUnitKey)[estimateUnitKey]; r.Value != "points" || r.Source != "default" {
			t.Fatalf("days --to %q wrote something: %+v", to, r)
		}
	}
	for _, c := range []struct{ to, unit string }{{"kb", "hours"}, {"kb/web", "hours"}, {"", "points"}} {
		h.MustYes(settings.OpPolicySet, setIn(estimateUnitKey, c.unit, c.to))
	}
}

// TestEstimateUnitLegacyDaysRowReads (OWNERQ-21, G-META-8): a days row
// written by an earlier build still resolves: policy show reads it, a task
// estimate under it is refused with the hint, a task without an estimate
// is written, and policy unset removes it (estimates then read in points).
func TestEstimateUnitLegacyDaysRowReads(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.Seed("policies", map[string]string{"key": `"` + estimateUnitKey + `"`, "value": `"days"`, "scope": `"repo"`})
	if r := showRules(t, h, estimateUnitKey)[estimateUnitKey]; r.Value != "days" || r.Level != "repo" {
		t.Fatalf("legacy row: %+v", r)
	}
	_, err := h.Run("task.add", `{"title":"estimated","estimate":"2"}`)
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "no day unit") {
		t.Fatalf("estimate under days: %v", err)
	}
	h.Must("task.add", `{"title":"no estimate"}`)
	h.MustYes(settings.OpPolicyUnset, `{"key":"`+estimateUnitKey+`"}`)
	if r := showRules(t, h, estimateUnitKey)[estimateUnitKey]; r.Value != "points" || r.Source != "default" {
		t.Fatalf("after unset: %+v", r)
	}
	h.Must("task.add", `{"title":"estimated","estimate":"2"}`)
}
