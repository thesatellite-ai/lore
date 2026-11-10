package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestPolicySetItemDomains (b-errs): the items of a set-shaped key are
// checked against that key's domain before anything is written:
// agents.person_only takes registered operation names or operation.field
// (a flag word, a CLI spelling, an unknown operation or field is
// E_INVALID_INPUT with a hint); routing.client_repos and meta.scope_aliases
// take name=value items. Valid values of each key are stored.
func TestPolicySetItemDomains(t *testing.T) {
	t.Parallel()
	cases := []struct {
		key, val string
		ok       bool
	}{
		{"agents.person_only", "true", false},
		{"agents.person_only", "false", false},
		{"agents.person_only", "task done", false},
		{"agents.person_only", "task.nope", false},
		{"agents.person_only", "task.done,bogus", false},
		{"agents.person_only", "meta.edit.no_such_field", false},
		{"agents.person_only", "task.done", true},
		{"agents.person_only", "task.delete, rule.add", true},
		{"agents.person_only", "meta.edit.merge_into", true},
		{"routing.client_repos", "rule", false},
		{"routing.client_repos", "rule=", false},
		{"routing.client_repos", "=agency", false},
		{"routing.client_repos", "rule=agency", true},
		{"meta.scope_aliases", "be", false},
		{"meta.scope_aliases", "be=repo:acmehub_backend", true},
	}
	for _, c := range cases {
		t.Run(c.key+"_"+strings.ReplaceAll(c.val, " ", "_"), func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t)
			_, err := h.RunYes(settings.OpPolicySet, setIn(c.key, c.val, ""))
			r := showRules(t, h, c.key)[c.key]
			if c.ok {
				if err != nil {
					t.Fatalf("%s=%s: %v", c.key, c.val, err)
				}
				if r.Source == "default" {
					t.Fatalf("%s=%s: nothing stored: %+v", c.key, c.val, r)
				}
				return
			}
			if !errs.Has(err, errs.InvalidInput) || errs.EnvelopeOf(err).Error.Hint == "" {
				t.Fatalf("%s=%s: %v, want E_INVALID_INPUT with a hint", c.key, c.val, err)
			}
			if r.Source != "default" {
				t.Fatalf("%s=%s refused but stored: %+v", c.key, c.val, r)
			}
		})
	}
}
