package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestPolicySetHintNamesValueType (G-META-13): a policy set whose value
// does not fit the key's type is refused E_INVALID_INPUT, stores nothing,
// and its hint names what that key takes, per shape: a rank key lists its
// choices, a flag says true or false, a limit says a whole number (with
// its ceiling when it has one), a weight gives its range.
func TestPolicySetHintNamesValueType(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ key, val, hint string }{
		{"secrets.on_detect", "maybe", "secrets.on_detect takes one of: refuse, ask, redact"},
		{"secrets.use_repo_gitleaks", "perhaps", "secrets.use_repo_gitleaks takes true or false (on or off)"},
		{"meta.max_bytes", "abc", "meta.max_bytes takes a whole number, 0 or more"},
		{"meta.max_bytes", "1.5", "meta.max_bytes takes a whole number, 0 or more"},
		{"context.warn_at", "250", "context.warn_at takes a whole number from 0 to 100"},
		{"tasks.urgency.age", "lots", "tasks.urgency.age takes a number with at most two decimals, from -1000 to 1000"},
		{"tasks.urgency.age", "1.234", "tasks.urgency.age takes a number with at most two decimals, from -1000 to 1000"},
	} {
		t.Run(c.key+"_"+c.val, func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t)
			_, err := h.RunYes(settings.OpPolicySet, setIn(c.key, c.val, ""))
			if !errs.Has(err, errs.InvalidInput) {
				t.Fatalf("%s=%s: %v, want E_INVALID_INPUT", c.key, c.val, err)
			}
			if got := errs.EnvelopeOf(err).Error.Hint; !strings.Contains(got, c.hint) {
				t.Fatalf("%s=%s hint %q, want %q", c.key, c.val, got, c.hint)
			}
			if r := showRules(t, h, c.key)[c.key]; r.Source != "default" {
				t.Fatalf("%s=%s refused but stored: %+v", c.key, c.val, r)
			}
		})
	}
}
