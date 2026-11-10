package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestPolicyUnsetAndPlaceRefusals: `policy unset` of a key that is no
// policy key is E_INVALID_INPUT like `policy set` (not "not set here"),
// before any row is read; a --to naming no attached place, for set and
// unset alike, is E_NOT_FOUND whose hint lists every place --to can name
// (a new folder of an attached store, kb/<folder>, is attached on demand,
// so it is no such refusal).
func TestPolicyUnsetAndPlaceRefusals(t *testing.T) {
	t.Parallel()
	h := multiStore(t)
	for _, in := range []string{`{"key":"no.such.key"}`, `{"key":"trash"}`, `{"key":"Trash.Retention_Days"}`, `{"key":"no.such.key","to":"kb"}`} {
		_, err := h.RunYes(settings.OpPolicyUnset, in)
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "unknown policy key") || errs.EnvelopeOf(err).Error.Hint == "" {
			t.Errorf("unset %s: E_INVALID_INPUT unknown policy key with a hint, got %v", in, err)
		}
	}
	for _, c := range []struct{ op, in string }{
		{settings.OpPolicySet, `{"key":"trash.retention_days","value":"30","to":"nowhere"}`},
		{settings.OpPolicySet, `{"key":"trash.retention_days","value":"30","to":"Kb"}`},
		{settings.OpPolicyUnset, `{"key":"trash.retention_days","to":"nowhere"}`},
	} {
		_, err := h.RunYes(c.op, c.in)
		hint := errs.EnvelopeOf(err).Error.Hint
		if !errs.Has(err, errs.NotFound) {
			t.Errorf("%s %s: E_NOT_FOUND, got %v", c.op, c.in, err)
			continue
		}
		for _, want := range []string{"--to repo", "--to kb", "--to kb/web", "--to kb/org", "--to agency/org", "lore store list"} {
			if !strings.Contains(hint, want) {
				t.Errorf("%s %s: the hint lists %q, got %q", c.op, c.in, want, hint)
			}
		}
	}
}
