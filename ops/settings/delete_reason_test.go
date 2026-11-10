package settings_test

import (
	"fmt"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestDeleteReasonFlags: under delete.require_reason every settings
// operation that removes a row takes --reason (field remove, meta forget,
// meta edit --remove-value and --values dropping a value). Without it the
// engine refuses with E_REASON_REQUIRED and nothing is removed; with it the
// same call runs. Before the flag these could never run under the policy.
func TestDeleteReasonFlags(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"server","name_format":"dns"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"note","type":"text"}`},
		[2]string{settings.OpMetaAdd, `{"name":"unused_one","type":"text"}`},
		[2]string{settings.OpMetaAdd, `{"name":"env","type":"text","values":"prod,dev,stage"}`},
		[2]string{settings.OpPolicySet, `{"key":"delete.require_reason","value":"true"}`},
	)
	cases := []struct {
		name, op, in string
	}{
		{"field remove", settings.OpFieldRemove, `{"kind":"server","field":"note"%s}`},
		{"meta forget", settings.OpMetaForget, `{"keys":"unused_one","confirm":true%s}`},
		{"meta edit --remove-value", settings.OpMetaEdit, `{"id":"env","remove_value":"dev"%s}`},
		{"meta edit --values", settings.OpMetaEdit, `{"id":"env","values":"prod"%s}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			failsYes(t, h, c.op, fmt.Sprintf(c.in, ""), errs.ReasonRequired) // said yes: still refused for the reason
			h.MustYes(c.op, fmt.Sprintf(c.in, `,"reason":"no longer used"`))
		})
	}
}
